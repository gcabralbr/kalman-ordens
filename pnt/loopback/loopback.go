// Package loopback confere, nos testes, que nenhum listener escuta fora do 127.0.0.1: um listener
// em todas as interfaces dispara o aviso do Firewall do Windows a cada `go test` (o executável de
// teste é novo a cada compilação) e expõe o PnT falso e a API na rede.
package loopback

import (
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"
)

// EnderecosExternos: os IPs não-loopback desta máquina.
func EnderecosExternos() []net.IP {
	var ips []net.IP
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			ips = append(ips, n.IP)
		}
	}
	return ips
}

// Expostas: as portas que respondem por algum IP não-loopback da máquina.
func Expostas(portas ...int) []string {
	var ruins []string
	for _, ip := range EnderecosExternos() {
		for _, p := range portas {
			end := net.JoinHostPort(ip.String(), strconv.Itoa(p))
			if c, err := net.DialTimeout("tcp", end, 300*time.Millisecond); err == nil {
				c.Close()
				ruins = append(ruins, end)
			}
		}
	}
	return ruins
}

// Exigir falha o teste se alguma das portas responder fora do loopback.
func Exigir(t testing.TB, portas ...int) {
	t.Helper()
	if len(EnderecosExternos()) == 0 {
		t.Log("loopback: a máquina não tem IP externo; conferência não feita")
		return
	}
	if r := Expostas(portas...); len(r) > 0 {
		t.Fatal(fmt.Sprintf("listener de teste fora do loopback (deve escutar só em 127.0.0.1): %v", r))
	}
}
