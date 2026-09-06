// Package nginxtunnel es el segundo "tunnel provider" de
// `asterion local tunnel start` (junto a Cloudflare Tunnel,
// ver cmd/asterion/local_tunnel.go): en vez de un proceso propio que
// Asterion arranca/mata (cloudflared), acá el "túnel" es un server block
// de un nginx YA instalado en la máquina, al que Asterion nunca reinicia
// ni reemplaza — solo agrega/quita SU PROPIO archivo de config y hace
// reload (nunca restart, para no cortar conexiones de otros sites que
// ese mismo nginx pueda estar sirviendo).
//
// Requiere que la máquina ya tenga un dominio público apuntando a ella y
// el puerto 80 (y 443, si se pide TLS) alcanzable desde internet —
// Asterion no gestiona DNS ni abre puertos en un firewall/security group,
// solo el lado de configuración de nginx.
package nginxtunnel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"asterion-core/internal/tunnel"
)

// Convención Debian/Ubuntu — mismo criterio que internal/osuser con
// Debian/Ubuntu para usuarios de sistema: si esta máquina no tiene este
// layout, error explícito en vez de adivinar una ruta que puede no ser
// la que nginx realmente lee acá (ej. RHEL usa conf.d sin sites-available).
const (
	sitesAvailableDir = "/etc/nginx/sites-available"
	sitesEnabledDir   = "/etc/nginx/sites-enabled"
	configName        = "asterion-tunnel.conf"
)

// Spec es lo que se pide publicar.
type Spec struct {
	Domain string
	Port   int
	Email  string // opcional — si viene, Start corre certbot --nginx después de habilitar el site
}

func configPath() string  { return filepath.Join(sitesAvailableDir, configName) }
func enabledPath() string { return filepath.Join(sitesEnabledDir, configName) }

// isSupportedLayout confirma que existen sites-available Y sites-enabled
// — nunca se asume, se comprueba en disco.
func isSupportedLayout() bool {
	if info, err := os.Stat(sitesAvailableDir); err != nil || !info.IsDir() {
		return false
	}
	if info, err := os.Stat(sitesEnabledDir); err != nil || !info.IsDir() {
		return false
	}
	return true
}

func findBinary(name, installHint string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("no encontré %q en el PATH — %s", name, installHint)
	}
	return path, nil
}

// renderServerBlock es deliberadamente mínimo: un solo location / con
// proxy_pass a 127.0.0.1:<port> y los headers estándar de reverse proxy.
// Nunca incluye 'listen 443' — eso lo agrega certbot --nginx si se pide
// TLS (spec.Email), para no duplicar esa lógica acá.
func renderServerBlock(domain string, port int) string {
	return fmt.Sprintf(`# Generado por 'asterion local tunnel start --tp nginx' — no editar a
# mano, se sobreescribe en cada 'start' y se borra entero con 'stop'.
server {
    listen 80;
    listen [::]:80;
    server_name %s;

    location / {
        proxy_pass http://127.0.0.1:%d;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
`, domain, port)
}

// Start escribe el server block y lo valida con 'nginx -t' ANTES de
// tocar nada más — si no valida, el archivo se borra y se devuelve el
// error de nginx tal cual (nunca se enmascara ni se reintenta distinto).
// Recién si valida se habilita (symlink) y se pide un reload — nunca un
// restart, que cortaría conexiones de cualquier otro site que este mismo
// nginx esté sirviendo. Con spec.Email, corre certbot --nginx al final
// (agrega el bloque 443 él mismo).
func Start(spec Spec) (tunnel.State, error) {
	if os.Geteuid() != 0 {
		return tunnel.State{}, fmt.Errorf("faltan privilegios: 'local tunnel start --tp nginx' necesita correr como root (sudo)")
	}
	if spec.Domain == "" {
		return tunnel.State{}, fmt.Errorf("--domain es obligatorio con --tp nginx")
	}
	if !isSupportedLayout() {
		return tunnel.State{}, fmt.Errorf(
			"nginx con el layout de Debian/Ubuntu (%s + %s) no está disponible en esta máquina — no soportado todavía",
			sitesAvailableDir, sitesEnabledDir,
		)
	}
	nginxBin, err := findBinary("nginx", "instalalo con tu gestor de paquetes (ej. 'apt install nginx')")
	if err != nil {
		return tunnel.State{}, err
	}

	if err := os.WriteFile(configPath(), []byte(renderServerBlock(spec.Domain, spec.Port)), 0o644); err != nil {
		return tunnel.State{}, fmt.Errorf("no pude escribir %s: %w", configPath(), err)
	}

	// El symlink tiene que existir ANTES de validar: 'nginx -t' solo
	// valida lo que está incluido de verdad en el árbol activo
	// (sites-enabled/*, por la convención Debian/Ubuntu) — validar el
	// archivo todavía en sites-available (sin symlink) sería una
	// validación vacía que siempre pasa, sin importar el contenido (bug
	// real encontrado y corregido en la verificación de esta feature:
	// un config con sintaxis inválida pasaba 'nginx -t' sin problema y
	// recién fallaba al hacer reload, con sites-enabled ya modificado).
	if _, err := os.Lstat(enabledPath()); os.IsNotExist(err) {
		if err := os.Symlink(configPath(), enabledPath()); err != nil {
			_ = os.Remove(configPath())
			return tunnel.State{}, fmt.Errorf("no pude habilitar el site (symlink): %w", err)
		}
	}

	if out, err := exec.Command(nginxBin, "-t").CombinedOutput(); err != nil {
		_ = os.Remove(enabledPath())
		_ = os.Remove(configPath())
		return tunnel.State{}, fmt.Errorf("el config generado no pasó 'nginx -t' — no se aplicó nada:\n%s", strings.TrimSpace(string(out)))
	}

	if err := reloadNginx(); err != nil {
		_ = os.Remove(enabledPath())
		_ = os.Remove(configPath())
		return tunnel.State{}, err
	}

	url := "http://" + spec.Domain
	if spec.Email != "" {
		certbotBin, err := findBinary("certbot", "instalalo con tu gestor de paquetes (ej. 'apt install certbot python3-certbot-nginx')")
		if err != nil {
			return tunnel.State{}, fmt.Errorf("nginx quedó publicado en HTTP (%s) pero no pude activar TLS: %w", url, err)
		}
		out, err := exec.Command(
			certbotBin, "--nginx", "-d", spec.Domain,
			"--non-interactive", "--agree-tos", "-m", spec.Email, "--redirect",
		).CombinedOutput()
		if err != nil {
			return tunnel.State{}, fmt.Errorf(
				"nginx quedó publicado en HTTP (%s) pero certbot falló — revisá que el dominio ya apunte de verdad a esta máquina y que el puerto 80 sea alcanzable desde internet:\n%s",
				url, strings.TrimSpace(string(out)),
			)
		}
		url = "https://" + spec.Domain
	}

	return tunnel.State{
		Provider:  "nginx",
		Port:      spec.Port,
		Domain:    spec.Domain,
		URL:       url,
		StartedAt: time.Now(),
	}, nil
}

// Stop borra SOLO lo que Start agregó (el symlink + el archivo de
// sites-available) — nunca toca otro server block que ya existiera, y
// hace reload (no restart). Los certificados que haya emitido certbot no
// se revocan: quedan en el sistema, listos si se vuelve a publicar el
// mismo dominio más adelante.
func Stop() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("faltan privilegios: 'local tunnel stop' con un túnel nginx necesita correr como root (sudo)")
	}
	_ = os.Remove(enabledPath())
	_ = os.Remove(configPath())
	return reloadNginx()
}

// IsAlive: a diferencia de Cloudflare Tunnel (un proceso con PID propio),
// acá "vivo" significa que el archivo que Start escribió sigue estando
// habilitado — nginx en sí es un service del sistema que Asterion nunca
// arranca ni para.
func IsAlive() bool {
	_, err := os.Lstat(enabledPath())
	return err == nil
}

// reloadNginx intenta systemctl primero (lo normal en Debian/Ubuntu con
// systemd) y cae a 'nginx -s reload' si no está disponible — nunca usa
// 'restart' en ninguno de los dos casos.
func reloadNginx() error {
	if out, err := exec.Command("systemctl", "reload", "nginx").CombinedOutput(); err != nil {
		if out2, err2 := exec.Command("nginx", "-s", "reload").CombinedOutput(); err2 != nil {
			return fmt.Errorf(
				"no pude recargar nginx (ni 'systemctl reload nginx' ni 'nginx -s reload'): %s / %s",
				strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)),
			)
		}
	}
	return nil
}
