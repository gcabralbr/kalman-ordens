package motor

import (
	"fmt"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// Bancada da HML (16/10): mensagens exploratórias ao PnT pela sessão do serviço, fora do fluxo de
// ordens. Só com ORDENS_AMBIENTE=HML. As respostas não mudam nada no motor; o registro é o monitor
// ao vivo (o fio cru).

// BancadaPosicoes: manda um RequestForPositions (35=AN) da conta e devolve, depois de espera, toda
// mensagem de aplicação que o PnT mandou nesse intervalo (AO, AP, j, 3...), crua.
func (m *Motor) BancadaPosicoes(conta string, espera time.Duration) Resposta {
	if m.p.Ambiente != usuario.HML {
		return Resposta{"aceito": false, "codigo": "SO_HML", "motivo": "bancada so com ORDENS_AMBIENTE=HML"}
	}
	if conta == "" || strings.ContainsAny(conta, "|=\x01") {
		return Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": "conta obrigatoria"}
	}
	var reqID, enviadaEm string
	var desde int64
	ok := false
	m.Fazer(func() {
		if m.fix == nil || !m.fix.Logado() {
			return
		}
		agora := m.agora()
		reqID = fmt.Sprintf("KM-POS-%d", agora.UnixMilli())
		dia := agora.In(relogio.Fuso).Format("20060102")
		id := pnt.Identificacao{Conta: conta, Bolsa: m.p.Bolsa, OnBehalfOfSubID: conta, SenderLocationID: m.p.SenderLocationID}
		desde = m.aoVivo.UltimoSeq()
		enviadaEm = emBR(agora)
		m.enfileirarEnvio(envioPendente{msg: pnt.PedidoPosicoes(id, reqID, dia, agora), clordid: reqID, prio: prioSaida})
		ok = true
	})
	if !ok {
		return Resposta{"aceito": false, "codigo": "SEM_SESSAO", "motivo": "sessao FIX nao logada"}
	}
	time.Sleep(espera)
	var env, rec []map[string]any
	for _, x := range m.aoVivo.Desde(desde) {
		switch {
		case x.Direcao == "ENV" && x.MsgType == "AN":
			env = append(env, map[string]any{"em": x.Em, "fix_raw": x.FixRaw})
		case x.Direcao == "REC" && x.MsgType != "0" && x.MsgType != "1":
			rec = append(rec, map[string]any{"em": x.Em, "msgtype": x.MsgType, "fix_raw": x.FixRaw})
		}
	}
	return Resposta{"aceito": true, "pos_req_id": reqID, "conta": conta, "enviada_em": enviadaEm, "espera_s": espera.Seconds(),
		"enviada": env, "recebidas": rec, "n_recebidas": len(rec)}
}
