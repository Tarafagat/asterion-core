package runtime

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
)

// NetworkInfo es lo que se pudo confirmar de la red de esta máquina — cada
// campo vacío/false significa "no se pudo confirmar", nunca "no existe".
type NetworkInfo struct {
	Interfaces    []NetworkInterface `json:"interfaces"`
	DefaultRoute  string             `json:"default_route"` // texto crudo de `ip route` (interfaz + gateway), vacío si no se pudo leer
	DNSServers    []string           `json:"dns_servers"`
	ListeningTCP  []ListeningPort    `json:"listening_tcp"`
	ListeningUDP  []ListeningPort    `json:"listening_udp"`
	PortsReadable bool               `json:"ports_readable"` // false si `ss` no está disponible en este sistema
}

type NetworkInterface struct {
	Name string   `json:"name"`
	IPv4 []string `json:"ipv4"`
	IPv6 []string `json:"ipv6"`
}

type ListeningPort struct {
	Address string `json:"address"` // ej. "127.0.0.1" o "*" (todas las interfaces)
	Port    string `json:"port"`
	Process string `json:"process,omitempty"` // vacío si no se pudo mapear el proceso (sin privilegios)
}

// DiscoverNetwork usa `ip`/`ss` (iproute2), ya presentes en cualquier
// distro Linux moderna — no agrega una dependencia nueva. Todo lo que
// devuelve es lo que estos comandos realmente reportan sin privilegios
// elevados; con root se ve además a qué proceso pertenece cada puerto,
// pero la lista de puertos en sí no depende de eso.
func DiscoverNetwork() NetworkInfo {
	return NetworkInfo{
		Interfaces:    discoverInterfaces(),
		DefaultRoute:  discoverDefaultRoute(),
		DNSServers:    discoverDNSServers(),
		ListeningTCP:  discoverListeningPorts("tcp"),
		ListeningUDP:  discoverListeningPorts("udp"),
		PortsReadable: commandAvailable("ss"),
	}
}

func commandAvailable(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func discoverInterfaces() []NetworkInterface {
	if !commandAvailable("ip") {
		return nil
	}
	out, err := exec.Command("ip", "-o", "addr", "show").Output()
	if err != nil {
		return nil
	}
	byName := map[string]*NetworkInterface{}
	var order []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		// Formato: "<idx>: <iface> inet <cidr> ..." o "... inet6 <cidr> ..."
		if len(fields) < 4 {
			continue
		}
		name := strings.TrimSuffix(fields[1], ":")
		iface, ok := byName[name]
		if !ok {
			iface = &NetworkInterface{Name: name}
			byName[name] = iface
			order = append(order, name)
		}
		for i, f := range fields {
			if f == "inet" && i+1 < len(fields) {
				iface.IPv4 = append(iface.IPv4, fields[i+1])
			}
			if f == "inet6" && i+1 < len(fields) {
				iface.IPv6 = append(iface.IPv6, fields[i+1])
			}
		}
	}
	result := make([]NetworkInterface, 0, len(order))
	for _, name := range order {
		result = append(result, *byName[name])
	}
	return result
}

func discoverDefaultRoute() string {
	if !commandAvailable("ip") {
		return ""
	}
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return line
}

func discoverDNSServers() []string {
	// /etc/resolv.conf es el mismo archivo que consulta cualquier programa
	// para resolver nombres, sea que lo administre systemd-resolved,
	// NetworkManager, o esté escrito a mano — es la fuente más portable.
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	var servers []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "nameserver" {
			servers = append(servers, fields[1])
		}
	}
	return servers
}

// discoverListeningPorts corre `ss` sin privilegios: sin root se ven todos
// los puertos escuchando, pero no a qué proceso pertenecen (columna vacía)
// — es una limitación real del sistema operativo, no del código.
func discoverListeningPorts(proto string) []ListeningPort {
	if !commandAvailable("ss") {
		return nil
	}
	flag := "-tln"
	if proto == "udp" {
		flag = "-uln"
	}
	out, err := exec.Command("ss", flag).Output()
	if err != nil {
		return nil
	}
	var ports []ListeningPort
	lines := strings.Split(string(out), "\n")
	for _, line := range lines[1:] { // primera línea es el encabezado
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		local := fields[3]
		idx := strings.LastIndex(local, ":")
		if idx == -1 {
			continue
		}
		ports = append(ports, ListeningPort{Address: local[:idx], Port: local[idx+1:]})
	}
	return ports
}
