package web

import (
	"net"
	"net/http"
	"net/netip"
)

// realIP подставляет в RemoteAddr адрес клиента из X-Real-IP, когда запрос
// пришёл от доверенного прокси. За nginx соединение всегда идёт с адреса
// шлюза docker, и без этого журнал входов и лимиты видели бы одного
// человека на всех.
//
// Берётся X-Real-IP, а не X-Forwarded-For: nginx его перезаписывает своим
// $remote_addr, а в X-Forwarded-For дописывает к тому, что прислал клиент,
// и первый адрес в нём подделывается.
func (s *Server) realIP(next http.Handler) http.Handler {
	if len(s.trustedProxies) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip, ok := s.clientIP(r); ok {
			r2 := *r
			_, port, _ := net.SplitHostPort(r.RemoteAddr)
			r2.RemoteAddr = net.JoinHostPort(ip.String(), port)
			r = &r2
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP — адрес из заголовка, если ему можно верить.
func (s *Server) clientIP(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	peer = peer.Unmap()
	trusted := false
	for _, p := range s.trustedProxies {
		if p.Contains(peer) {
			trusted = true
			break
		}
	}
	if !trusted {
		return netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(r.Header.Get("X-Real-IP"))
	if err != nil {
		return netip.Addr{}, false
	}
	return ip.WithZone("").Unmap(), true
}
