package flexscan

import (
	"crypto/subtle"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TamanhoMinimoSegredo do FLEXSCAN_WEBHOOK_SEGREDO (SPEC-FLEXSCAN-v1 §5.1).
const TamanhoMinimoSegredo = 32

const maxCorpoWebhook = 64 << 10

// Receptor: a rota do webhook (SPEC §5). Um handler com uma rota só — POST <prefixo><segredo> —,
// servido num listener próprio (FLEXSCAN_WEBHOOK_ENDERECO), nunca no mux do admin. Qualquer outra
// coisa (segredo, método ou caminho errados) → 404 sem corpo, igual para todos.
type Receptor struct {
	ponte   *Ponte
	caminho string

	mu        sync.Mutex
	recusas   map[string]int64 // por motivo (contador; o /metrics lê)
	ultimoLog time.Time
}

// NovoReceptor: prefixo ("/flexscan/webhook/") + segredo; o segredo nunca é registrado.
func NovoReceptor(p *Ponte, prefixo, segredo string) (*Receptor, error) {
	if len(segredo) < TamanhoMinimoSegredo {
		return nil, fmt.Errorf("FLEXSCAN_WEBHOOK_SEGREDO com menos de %d caracteres", TamanhoMinimoSegredo)
	}
	if strings.ContainsAny(segredo, "/?#% ") {
		return nil, fmt.Errorf("FLEXSCAN_WEBHOOK_SEGREDO com caractere inválido para caminho (use base64url)")
	}
	if !strings.HasPrefix(prefixo, "/") || !strings.HasSuffix(prefixo, "/") {
		return nil, fmt.Errorf("FLEXSCAN_WEBHOOK_PREFIXO %q: começa e termina com /", prefixo)
	}
	return &Receptor{ponte: p, caminho: prefixo + segredo, recusas: map[string]int64{}}, nil
}

// Recusas: contador por motivo (métrica flexscan_webhook_recusados_total).
func (r *Receptor) Recusas() map[string]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := make(map[string]int64, len(r.recusas))
	for k, v := range r.recusas {
		c[k] = v
	}
	return c
}

func (r *Receptor) recusar(w http.ResponseWriter, req *http.Request, motivo string) {
	r.mu.Lock()
	r.recusas[motivo]++
	logar := time.Since(r.ultimoLog) >= time.Minute
	if logar {
		r.ultimoLog = time.Now()
	}
	r.mu.Unlock()
	if logar {
		// no máximo uma linha por minuto; sem o caminho (pode ter o segredo, certo ou errado)
		slog.Warn("flexscan: webhook recusado", "motivo", motivo, "origem", origem(req), "metodo", req.Method)
	}
	r.ponte.WebhookRecusado(motivo)
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusNotFound)
}

// origem: o IP de quem chamou (o nginx põe o do cliente no X-Real-IP).
func origem(req *http.Request) string {
	if v := req.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	h, _, _ := net.SplitHostPort(req.RemoteAddr)
	return h
}

func (r *Receptor) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if subtle.ConstantTimeCompare([]byte(req.URL.Path), []byte(r.caminho)) != 1 {
		r.recusar(w, req, "caminho")
		return
	}
	if req.Method != http.MethodPost {
		r.recusar(w, req, "metodo")
		return
	}
	corpo, err := io.ReadAll(io.LimitReader(req.Body, maxCorpoWebhook+1))
	if err != nil {
		r.recusar(w, req, "leitura")
		return
	}
	if len(corpo) > maxCorpoWebhook {
		r.recusar(w, req, "tamanho")
		return
	}
	// forma desconhecida: 200 assim mesmo (não provoca reenvio em laço); a ponte alerta e consulta
	_ = r.ponte.ReceberWebhook(corpo, origem(req))
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}
