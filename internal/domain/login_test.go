package domain

import "testing"

// Маска — главное решение всей затеи: от неё зависит, помечен будет
// человек с двумя устройствами или человек, раздавший пароль.
func TestParseLoginPlace(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		ip, subnet string
	}{
		{"адрес с портом", "203.0.113.9:52344", "203.0.113.9", "203.0.113.0/24"},
		{"адрес без порта", "203.0.113.9", "203.0.113.9", "203.0.113.0/24"},
		{"ipv6 сворачивается по /48", "[2001:db8:1234:5678::1]:443",
			"2001:db8:1234:5678::1", "2001:db8:1234::/48"},
		// Зона в inet не поедет, а к месту ничего не добавляет.
		{"ipv6 с зоной", "[fe80::1%eth0]:443", "fe80::1", "fe80::/48"},
		// «::ffff:1.2.3.4» — тот же IPv4, и подсеть у него ipv4-шная:
		// иначе один и тот же вход из одного и того же места давал бы
		// две разные подсети.
		{"ipv4 в обёртке ipv6", "::ffff:203.0.113.9", "203.0.113.9", "203.0.113.0/24"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			place, err := ParseLoginPlace(tt.in)
			if err != nil {
				t.Fatalf("разбор %q: %v", tt.in, err)
			}
			if place.IP != tt.ip {
				t.Errorf("адрес: получили %q, ждали %q", place.IP, tt.ip)
			}
			if place.Subnet != tt.subnet {
				t.Errorf("подсеть: получили %q, ждали %q", place.Subnet, tt.subnet)
			}
		})
	}
}

// Соседние адреса одного провайдера — одно место, иначе метка ловила бы
// смену адреса, а не раздачу доступа.
func TestSubnetFoldsNeighbours(t *testing.T) {
	a, err := ParseLoginPlace("203.0.113.9:1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseLoginPlace("203.0.113.200:2")
	if err != nil {
		t.Fatal(err)
	}
	if a.Subnet != b.Subnet {
		t.Errorf("соседи оказались в разных подсетях: %s и %s", a.Subnet, b.Subnet)
	}
	// А другая сеть — уже другое место.
	c, err := ParseLoginPlace("198.51.100.9:3")
	if err != nil {
		t.Fatal(err)
	}
	if a.Subnet == c.Subnet {
		t.Errorf("разные сети слились в одну: %s", a.Subnet)
	}
}

// Непонятный адрес — не повод записать в журнал «вход откуда-то»:
// пустое место вызывающий отличает от разобранного и просто не пишет.
func TestParseLoginPlaceRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "не адрес", "localhost:8080", "1.2.3:80"} {
		if place, err := ParseLoginPlace(in); err == nil {
			t.Errorf("%q разобрался в %+v, а не должен", in, place)
		}
	}
}
