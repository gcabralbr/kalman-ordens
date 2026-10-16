package motor

import (
	"fmt"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// BenchmarkSinalParte: uma parte de 1.000 entradas (uids distintos) com 3.000 operações no dia.
func BenchmarkSinalParte(b *testing.B) {
	t := &testing.T{}
	m, _, mem, _ := novoMotorTeste(t, map[string]string{"LIMITE_POSICAO_MAX": "1000000", "LIMITE_INTERVALO_ENTRADAS_S": "0",
		"LIMITE_ENTRADAS_MIN": "1000000", "LIMITE_MENSAGENS_MIN": "100000000", "LIMITE_ENTRADAS_DIA": "1000000", "FIX_TAXA_MSG_S": "100000"})
	for u := 1000; u < 1000+4000*(b.N+1); u++ {
		_ = mem.DefinirUsuario(u, "hml", `{"Robo":{"CORSeqId":85,"CCONroConta":"12345"}}`)
		_ = mem.GravarHabilitacao(dominio.Habilitacao{UID: u, Habilitado: true, Por: "b", Em: time.Now()})
	}
	m.InvalidarUsuarios()
	op := int64(1)
	parte := func(id string) PedidoSinal {
		p := PedidoSinal{SinalID: id, TsDecisao: "t", Parte: ptr(1), Partes: ptr(1)}
		for i := 0; i < 1000; i++ {
			p.Entradas = append(p.Entradas, []byte(fmt.Sprintf(`{"operacao_id":%d,"uid":%d,"lado":"C","qtd":1,"stop_pts":500,"motivo":"SINAL"}`, op, 1000+op)))
			op++
		}
		return p
	}
	for i := 0; i < 3; i++ {
		m.Sinal(parte(fmt.Sprint("pre", i)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Sinal(parte(fmt.Sprint("b", i)))
	}
}
