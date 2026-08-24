// Package sysinfo lee el estado real de la máquina donde corre el CLI:
// datos crudos (CPU, RAM, disco, red, qué sistema es) — sin costo ni
// tarifa mezclada acá adentro. Calcular cuánto sale eso es un problema
// aparte que resuelve el pricing engine del lado de Asterion Cloud, con
// la tarifa vigente del proyecto; este paquete solo reporta lo que un
// analista de sistema mediría con las herramientas del propio sistema
// operativo. Usado tanto por `asterion local` (consulta puntual) como por
// `asterion agent-run` (el mismo dato, en loop, empujado a la nube).
//
// Linux y macOS por ahora (Windows queda pendiente): cada uno lee las
// fuentes nativas del sistema operativo — /proc y /sys en Linux, sysctl/
// vm_stat/netstat en macOS — en vez de traer una librería multiplataforma,
// así el binario sigue sin dependencias.
package sysinfo

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Snapshot es una medición puntual de uso — todo en unidades crudas
// (porcentaje o GB), nunca un costo.
type Snapshot struct {
	CPUPercent   float64   `json:"cpu_percent"`
	RAMUsedGB    float64   `json:"ram_used_gb"`
	RAMTotalGB   float64   `json:"ram_total_gb"`
	DiskUsedGB   float64   `json:"disk_used_gb"`
	DiskTotalGB  float64   `json:"disk_total_gb"`
	NetworkInGB  float64   `json:"network_in_gb"`  // acumulado recibido desde que arrancó la máquina (todas las interfaces salvo loopback)
	NetworkOutGB float64   `json:"network_out_gb"` // ídem, enviado
	TakenAt      time.Time `json:"taken_at"`
}

// Info es la identidad de la máquina: qué es, no cuánto está usando.
type Info struct {
	Hostname       string  `json:"hostname"`
	OS             string  `json:"os"`
	Architecture   string  `json:"architecture"`
	KernelVersion  string  `json:"kernel_version,omitempty"`
	CPUModel       string  `json:"cpu_model,omitempty"`
	CPUCores       int     `json:"cpu_cores"`
	RAMTotalGB     float64 `json:"ram_total_gb"`
	DiskTotalGB    float64 `json:"disk_total_gb"`
	Virtualization string  `json:"virtualization"` // "container" | "virtual-machine" | "physical" | "unknown" (heurística, no garantizada)
}

// Collect toma una medición real ahora mismo. El cálculo de %CPU necesita
// dos lecturas de /proc/stat separadas por un intervalo corto (200ms) —
// por eso esta llamada tarda ~200ms, es esperable.
func Collect() (Snapshot, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return Snapshot{}, fmt.Errorf("sysinfo todavía no soporta %s (solo Linux y macOS por ahora)", runtime.GOOS)
	}

	snap := Snapshot{TakenAt: time.Now()}

	if cpu, err := cpuPercent(); err == nil {
		snap.CPUPercent = cpu
	}
	if usedGB, totalGB, err := ramGB(); err == nil {
		snap.RAMUsedGB, snap.RAMTotalGB = usedGB, totalGB
	}
	if usedGB, totalGB, err := diskGB("/"); err == nil {
		snap.DiskUsedGB, snap.DiskTotalGB = usedGB, totalGB
	}
	if inGB, outGB, err := networkGB(); err == nil {
		snap.NetworkInGB, snap.NetworkOutGB = inGB, outGB
	}
	return snap, nil
}

// GatherInfo describe la máquina (no cambia de una llamada a otra, salvo
// que se le agregue/saque hardware).
func GatherInfo() (Info, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return Info{}, fmt.Errorf("sysinfo todavía no soporta %s (solo Linux y macOS por ahora)", runtime.GOOS)
	}

	hostname, _ := os.Hostname()
	_, ramTotalGB, _ := ramGB()
	_, diskTotalGB, _ := diskGB("/")

	return Info{
		Hostname:       hostname,
		OS:             runtime.GOOS,
		Architecture:   runtime.GOARCH,
		KernelVersion:  kernelVersion(),
		CPUModel:       cpuModel(),
		CPUCores:       runtime.NumCPU(),
		RAMTotalGB:     ramTotalGB,
		DiskTotalGB:    diskTotalGB,
		Virtualization: detectVirtualization(),
	}, nil
}

// runSysctl corre `sysctl -n <name>` y devuelve el valor sin el salto de
// línea final — usado para todo lo que macOS solo expone por sysctl
// (memoria total, modelo de CPU, versión de kernel, presencia de
// hipervisor). Un solo helper en vez de mezclar syscall.Sysctl (que
// devuelve basura para sysctls numéricos como hw.memsize, tratados como
// bytes en vez de string) con exec.Command caso por caso.
func runSysctl(name string) (string, error) {
	out, err := exec.Command("sysctl", "-n", name).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func readProcStatTotals() (idle, total uint64, err error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 8 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("formato inesperado de /proc/stat")
	}
	var sum uint64
	for _, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		sum += v
	}
	idleVal, _ := strconv.ParseUint(fields[4], 10, 64)
	return idleVal, sum, nil
}

func cpuPercent() (float64, error) {
	if runtime.GOOS == "darwin" {
		return cpuPercentDarwin()
	}
	idle1, total1, err := readProcStatTotals()
	if err != nil {
		return 0, err
	}
	time.Sleep(200 * time.Millisecond)
	idle2, total2, err := readProcStatTotals()
	if err != nil {
		return 0, err
	}

	deltaTotal := float64(total2 - total1)
	deltaIdle := float64(idle2 - idle1)
	if deltaTotal <= 0 {
		return 0, nil
	}
	return (1 - deltaIdle/deltaTotal) * 100, nil
}

// cpuPercentDarwin usa `top -l 1` — a diferencia de /proc/stat en Linux, no
// hace falta tomar dos muestras a mano: top ya calcula el delta
// internamente antes de imprimir la línea "CPU usage".
func cpuPercentDarwin() (float64, error) {
	out, err := exec.Command("top", "-l", "1", "-n", "0", "-s", "0").Output()
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "CPU usage:") {
			continue
		}
		// "CPU usage: 9.93% user, 15.56% sys, 74.50% idle"
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "idle" && i > 0 {
				idleStr := strings.TrimSuffix(strings.TrimSuffix(fields[i-1], "%,"), "%")
				idle, err := strconv.ParseFloat(idleStr, 64)
				if err != nil {
					return 0, err
				}
				return 100 - idle, nil
			}
		}
	}
	return 0, fmt.Errorf("no encontré la línea 'CPU usage:' en la salida de top")
}

func cpuModel() string {
	if runtime.GOOS == "darwin" {
		model, _ := runSysctl("machdep.cpu.brand_string")
		return model
	}
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func kernelVersion() string {
	if runtime.GOOS == "darwin" {
		version, _ := runSysctl("kern.osrelease")
		return version
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		return fields[2]
	}
	return ""
}

func ramGB() (usedGB, totalGB float64, err error) {
	if runtime.GOOS == "darwin" {
		return ramGBDarwin()
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	var totalKB, availableKB float64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			totalKB = value
		case "MemAvailable:":
			availableKB = value
		}
	}
	totalGB = totalKB / 1024 / 1024
	usedGB = (totalKB - availableKB) / 1024 / 1024
	return usedGB, totalGB, nil
}

// ramGBDarwin: total sale de sysctl hw.memsize (bytes, exacto). Usado es
// una aproximación deliberada — a diferencia de Linux (que expone
// MemAvailable directo), macOS no tiene un único número de "memoria
// disponible": vm_stat separa páginas libres/activas/inactivas/
// especulativas/wired/comprimidas, y una porción de las "inactivas" es en
// realidad caché reclamable (lo mismo que hace que Activity Monitor casi
// nunca muestre "libre" un número grande). Se usa activas + wired +
// comprimidas como "usado", el mismo criterio que Activity Monitor llama
// "Memory Used" — es una heurística razonable, no un valor exacto como el
// de Linux.
func ramGBDarwin() (usedGB, totalGB float64, err error) {
	memsizeStr, err := runSysctl("hw.memsize")
	if err != nil {
		return 0, 0, err
	}
	totalBytes, err := strconv.ParseFloat(memsizeStr, 64)
	if err != nil {
		return 0, 0, err
	}
	totalGB = totalBytes / 1024 / 1024 / 1024

	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, totalGB, err
	}
	pageSize := 4096.0
	var active, wired, compressed float64
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Mach Virtual Memory Statistics:") {
			// "Mach Virtual Memory Statistics: (page size of 16384 bytes)"
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "of" && i+1 < len(fields) {
					if v, err := strconv.ParseFloat(fields[i+1], 64); err == nil {
						pageSize = v
					}
				}
			}
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		label := strings.TrimSpace(parts[0])
		value, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[1]), ".")), 64)
		if err != nil {
			continue
		}
		switch label {
		case "Pages active":
			active = value
		case "Pages wired down":
			wired = value
		case "Pages occupied by compressor":
			compressed = value
		}
	}
	usedGB = (active + wired + compressed) * pageSize / 1024 / 1024 / 1024
	return usedGB, totalGB, nil
}

func diskGB(path string) (usedGB, totalGB float64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	total := float64(stat.Blocks) * float64(stat.Bsize)
	free := float64(stat.Bavail) * float64(stat.Bsize)
	totalGB = total / 1024 / 1024 / 1024
	usedGB = (total - free) / 1024 / 1024 / 1024
	return usedGB, totalGB, nil
}

// networkGB suma bytes recibidos/enviados de todas las interfaces salvo
// loopback, desde que la máquina arrancó (son contadores acumulativos del
// kernel, no una tasa) — "la cantidad de datos", cruda, sin costo.
func networkGB() (inGB, outGB float64, err error) {
	if runtime.GOOS == "darwin" {
		return networkGBDarwin()
	}
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0, err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 3 {
		return 0, 0, fmt.Errorf("formato inesperado de /proc/net/dev")
	}

	var totalRxBytes, totalTxBytes float64
	for _, line := range lines[2:] {
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" || iface == "" {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}
		rx, err1 := strconv.ParseFloat(fields[0], 64)
		tx, err2 := strconv.ParseFloat(fields[8], 64)
		if err1 == nil {
			totalRxBytes += rx
		}
		if err2 == nil {
			totalTxBytes += tx
		}
	}
	return totalRxBytes / 1024 / 1024 / 1024, totalTxBytes / 1024 / 1024 / 1024, nil
}

// networkGBDarwin suma bytes de `netstat -ib`, una fila por interfaz (la
// fila "<Link#N>" — hay filas adicionales por cada protocolo/dirección IP
// configurada en la misma interfaz que repiten el mismo contador
// acumulado, así que sumar todas las filas duplicaría el conteo; se toma
// solo la fila de Link, la única garantizada por interfaz). El nombre y el
// MTU siempre están presentes, así que la columna "Network" (donde
// aparece "<Link#N>") queda siempre en fields[2] aunque falte la columna
// Address (pasa en interfaces sin MAC, como gif0/stf0) — separado de eso,
// las últimas 7 columnas (Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll) son
// siempre numéricas en la fila de Link, a diferencia de las filas de
// protocolo que muestran "-" en vez de un valor.
func networkGBDarwin() (inGB, outGB float64, err error) {
	out, err := exec.Command("netstat", "-ib").Output()
	if err != nil {
		return 0, 0, err
	}
	var totalRxBytes, totalTxBytes float64
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		iface := strings.TrimSuffix(fields[0], "*") // "*" = interfaz caída
		if iface == "lo0" || iface == "Name" {
			continue
		}
		if !strings.HasPrefix(fields[2], "<Link") {
			continue
		}
		numeric := fields[len(fields)-7:]
		rx, err1 := strconv.ParseFloat(numeric[2], 64)
		tx, err2 := strconv.ParseFloat(numeric[5], 64)
		if err1 == nil {
			totalRxBytes += rx
		}
		if err2 == nil {
			totalTxBytes += tx
		}
	}
	return totalRxBytes / 1024 / 1024 / 1024, totalTxBytes / 1024 / 1024 / 1024, nil
}

// detectVirtualization es una heurística de mejor esfuerzo: no siempre
// puede saberlo con certeza (algunos archivos requieren permisos), en ese
// caso devuelve "unknown" en vez de adivinar.
func detectVirtualization() string {
	if runtime.GOOS == "darwin" {
		return detectVirtualizationDarwin()
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "container"
	}
	if cgroup, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(cgroup)
		if strings.Contains(s, "docker") || strings.Contains(s, "kubepods") || strings.Contains(s, "containerd") {
			return "container"
		}
	}

	vmMarkers := []string{"QEMU", "VMware", "VirtualBox", "KVM", "Xen", "Microsoft Corporation", "Google", "Amazon EC2"}
	for _, path := range []string{"/sys/class/dmi/id/product_name", "/sys/class/dmi/id/sys_vendor"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		value := strings.TrimSpace(string(data))
		for _, marker := range vmMarkers {
			if strings.Contains(value, marker) {
				return "virtual-machine"
			}
		}
		if value != "" {
			return "physical"
		}
	}
	return "unknown"
}

// detectVirtualizationDarwin usa kern.hv_vmm_present — un sysctl del
// propio kernel de macOS que indica si ESTE sistema operativo está
// corriendo como huésped de un hipervisor (Parallels, VMware, el
// Virtualization.framework de Apple, etc.), a diferencia de la heurística
// de DMI que usa Linux (que solo puede inferirlo de metadata del hardware
// reportada). Si el sysctl no existe o no se puede leer, "unknown" en vez
// de asumir "physical".
func detectVirtualizationDarwin() string {
	value, err := runSysctl("kern.hv_vmm_present")
	if err != nil {
		return "unknown"
	}
	if value == "1" {
		return "virtual-machine"
	}
	if value == "0" {
		return "physical"
	}
	return "unknown"
}
