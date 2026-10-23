package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestRealIP(t *testing.T) {
	proxy := []netip.Prefix{netip.MustParsePrefix("172.19.0.1/32")}

	tests := []struct {
		name    string
		trusted []netip.Prefix
		remote  string
		header  string
		want    string
	}{
		{"от прокси — адрес клиента", proxy, "172.19.0.1:40000", "203.0.113.9", "203.0.113.9:40000"},
		// Заголовок от кого попало — подделка: адрес соединения остаётся.
		{"не от прокси — заголовок мимо", proxy, "198.51.100.7:5555", "203.0.113.9", "198.51.100.7:5555"},
		{"прокси без заголовка", proxy, "172.19.0.1:40000", "", "172.19.0.1:40000"},
		{"мусор в заголовке", proxy, "172.19.0.1:40000", "not-an-ip", "172.19.0.1:40000"},
		{"ipv6 клиента", proxy, "172.19.0.1:40000", "2001:db8::1", "[2001:db8::1]:40000"},
		// Без настройки заголовок не читается вовсе — как было до прокси.
		{"без настройки", nil, "172.19.0.1:40000", "203.0.113.9", "172.19.0.1:40000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{trustedProxies: tt.trusted}
			var got string
			h := s.realIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			}))
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remote
			if tt.header != "" {
				r.Header.Set("X-Real-IP", tt.header)
			}
			h.ServeHTTP(httptest.NewRecorder(), r)
			if got != tt.want {
				t.Errorf("RemoteAddr = %q, ожидали %q", got, tt.want)
			}
		})
	}
}
