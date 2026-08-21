package runtime

import "strconv"

// Check es un ítem individual del reporte de `asterion local doctor` —
// Pass puede ser true (✓), false (✗) o nil cuando no se pudo determinar
// (por ejemplo en un sistema operativo no soportado todavía), para no
// confundir "no lo sabemos" con "está mal".
type Check struct {
	Name   string `json:"name"`
	Pass   *bool  `json:"pass"`
	Detail string `json:"detail"`
}

// DoctorReport agrupa los checks como el ejemplo del spec (§22): Runtime,
// Network/Security, Reverse Proxy, Tunnel, Service, más SSH/Network/Risk
// (spec "Infrastructure Safety Lab" §10-11).
type DoctorReport struct {
	Runtime      []Check           `json:"runtime"`
	SSH          []Check           `json:"ssh"`
	Network      []Check           `json:"network"`
	Security     []Check           `json:"security"`
	ReverseProxy []Check           `json:"reverse_proxy"`
	Tunnel       []Check           `json:"tunnel"`
	Service      []Check           `json:"service"`
	SSHRisk      SSHRiskAssessment `json:"ssh_risk"`
	Healthy      bool              `json:"healthy"`
}

func boolPtr(b bool) *bool { return &b }

func passCheck(name, detailIfTrue, detailIfFalse string, ok bool) Check {
	if ok {
		return Check{Name: name, Pass: boolPtr(true), Detail: detailIfTrue}
	}
	return Check{Name: name, Pass: boolPtr(false), Detail: detailIfFalse}
}

// RunDoctor arma el reporte de salud a partir de un Environment ya
// descubierto y la Config vigente — no vuelve a hacer discovery, para que
// `doctor` y `status` compartan siempre la misma lectura de la máquina en
// una sola corrida.
func RunDoctor(env Environment, cfg Config) DoctorReport {
	report := DoctorReport{}

	portOpen := PortListening(cfg.ServiceBind, cfg.ServicePort)
	portStr := strconv.Itoa(cfg.ServicePort)
	report.Runtime = append(report.Runtime,
		passCheck("Puerto de la aplicación",
			"Escuchando en "+cfg.ServiceBind+":"+portStr,
			"Nada responde en "+cfg.ServiceBind+":"+portStr+" — ¿corriste 'asterion local serve'?",
			portOpen),
		passCheck("Service manager detectado",
			env.ServiceManager, "No se detectó systemd/launchd — la instalación como servicio no está disponible en este sistema",
			env.ServiceManager != "unknown"),
	)

	publiclyExposed := cfg.ServiceBind != "127.0.0.1" && cfg.ServiceBind != "localhost"
	report.Security = append(report.Security,
		passCheck("Puerto no expuesto directamente",
			"service_bind = "+cfg.ServiceBind+" (solo loopback)",
			"service_bind = "+cfg.ServiceBind+" — el puerto de la app escucha más allá de localhost sin un reverse proxy delante",
			!publiclyExposed),
		passCheck("Remote management",
			remoteManagementDetail(cfg),
			remoteManagementDetail(cfg),
			true), // informativo, no es un fallo que esté apagado — es el default seguro
	)

	if len(env.ReverseProxy) > 0 {
		for _, name := range env.ReverseProxy {
			report.ReverseProxy = append(report.ReverseProxy, passCheck("Reverse proxy detectado", name, "", true))
		}
	} else {
		report.ReverseProxy = append(report.ReverseProxy, Check{Name: "Reverse proxy", Pass: nil, Detail: "No se detectó ninguno — la app queda accesible solo en localhost, lo cual está bien para uso local"})
	}

	if len(env.Tunnel) > 0 {
		for _, name := range env.Tunnel {
			report.Tunnel = append(report.Tunnel, passCheck("Tunnel detectado", name, "", true))
		}
	} else {
		report.Tunnel = append(report.Tunnel, Check{Name: "Tunnel", Pass: nil, Detail: "No se detectó ninguno"})
	}

	fwInspection := InspectFirewall(env.Firewall)
	report.Service = append(report.Service,
		passCheck("Firewall detectado", joinOrNone(env.Firewall), "No se detectó ningún firewall administrable conocido (ufw/nftables/iptables/firewalld)", len(env.Firewall) > 0),
	)
	if len(env.Firewall) > 0 {
		report.Service = append(report.Service, passCheck(
			"Estado del firewall legible",
			fwInspection.Backend+": "+shortSummary(fwInspection.RawRules),
			fwInspection.Detail,
			fwInspection.Readable,
		))
	}

	ssh := DiscoverSSH()
	report.SSH = append(report.SSH,
		passCheck("Servicio SSH", "activo, puerto(s): "+joinOrNone(ssh.Ports)+" (fuente: "+ssh.PortSource+")",
			"no se detectó un servicio SSH activo en esta máquina", ssh.ServiceActive),
	)
	if ssh.ServiceActive {
		report.SSH = append(report.SSH, Check{
			Name: "Sesiones activas", Pass: nil,
			Detail: strconv.Itoa(len(ssh.CurrentSessions)) + " sesión(es) — ver 'who'",
		})
		if ssh.RunningOverSSH {
			report.SSH = append(report.SSH, Check{Name: "Esta sesión", Pass: nil, Detail: "asterion está corriendo dentro de una conexión SSH ahora mismo"})
		}
	}

	risk := AssessSSHRisk(ssh, fwInspection)
	report.SSHRisk = risk

	net := DiscoverNetwork()
	report.Network = append(report.Network,
		Check{Name: "Interfaces", Pass: nil, Detail: strconv.Itoa(len(net.Interfaces)) + " detectada(s)"},
		Check{Name: "Ruta por defecto", Pass: nil, Detail: emptyOr(net.DefaultRoute, "no se pudo determinar")},
		Check{Name: "DNS", Pass: nil, Detail: joinOrNone(net.DNSServers)},
		passCheck("Puertos escuchando legibles", strconv.Itoa(len(net.ListeningTCP))+" TCP, "+strconv.Itoa(len(net.ListeningUDP))+" UDP",
			"el comando 'ss' no está disponible en este sistema", net.PortsReadable),
	)

	report.Healthy = allPass(report.Runtime) && allPass(report.Security) && risk.Level != RiskCritical
	return report
}

func shortSummary(raw string) string {
	if raw == "" {
		return "(sin salida)"
	}
	lines := 0
	for i, c := range raw {
		if c == '\n' {
			lines++
			if lines >= 3 {
				return raw[:i] + " …"
			}
		}
	}
	return raw
}

func emptyOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func remoteManagementDetail(cfg Config) string {
	if cfg.RemoteManagement {
		return "habilitado"
	}
	return "deshabilitado (default) — Asterion Cloud no puede modificar nada de esta máquina hasta que lo actives"
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "ninguno"
	}
	out := items[0]
	for _, item := range items[1:] {
		out += ", " + item
	}
	return out
}

func allPass(checks []Check) bool {
	for _, c := range checks {
		if c.Pass != nil && !*c.Pass {
			return false
		}
	}
	return true
}
