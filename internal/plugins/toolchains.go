// Herramientas ("toolchains") que un plugin declara necesitar
// (`requires=["node@20.11.0"]` en System.plugin — ver
// asterion-language/systemspec y cmd/asterion/plugin_system.go) pero que
// no vienen con el propio Go de este binario. Mismo espíritu que el venv
// de Python en build.go: nunca toca un package manager del sistema
// (nada de apt/brew/choco, nada de sudo) — descarga la distribución
// oficial pre-compilada, la verifica contra el checksum oficial, y la
// extrae en un directorio propio de Asterion. Nunca se agrega al PATH
// del sistema ni reemplaza un Node que el usuario ya tenga instalado —
// cmd/asterion/plugin_system.go antepone este bin/ al PATH solo del
// exec.Command puntual que arranca/compila ESE plugin.
package plugins

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var nodeVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// toolchainsDir es ~/.config/asterion/plugins/toolchains — un directorio
// más al lado de repos/config/logs, mismo BaseDir que todo lo demás de
// este paquete.
func toolchainsDir() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "toolchains")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// nodePlatform traduce runtime.GOOS/GOARCH al vocabulario que usa
// nodejs.org para nombrar sus tarballs — "win" no "windows", "x64" no
// "amd64".
func nodePlatform() (osName, archName string, err error) {
	switch runtime.GOOS {
	case "linux":
		osName = "linux"
	case "darwin":
		osName = "darwin"
	case "windows":
		osName = "win"
	default:
		return "", "", fmt.Errorf("SO %q no soportado para descargar un Node sandboxed", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		archName = "x64"
	case "arm64":
		archName = "arm64"
	default:
		return "", "", fmt.Errorf("arquitectura %q no soportada para descargar un Node sandboxed", runtime.GOARCH)
	}
	return osName, archName, nil
}

// EnsureNode devuelve la ruta al directorio bin/ de una distribución de
// Node ya lista para usar en version exacta — la descarga/verifica/
// extrae si todavía no existe (idempotente, mismo criterio que
// EnsureContractRepo). version SIEMPRE tiene que ser exacta (ej.
// "20.11.0") — nunca se adivina "la LTS actual" (eso se pudre con el
// tiempo): pedir "node" a secas o una versión mal formada es un error
// claro, no un default silencioso.
func EnsureNode(version string) (binDir string, err error) {
	if !nodeVersionPattern.MatchString(version) {
		return "", fmt.Errorf(
			"versión de Node inválida: %q — tiene que ser exacta, tipo \"20.11.0\" (nunca \"node\" a secas ni \"lts\": Asterion nunca adivina cuál es \"la actual\")",
			version,
		)
	}
	osName, archName, err := nodePlatform()
	if err != nil {
		return "", err
	}

	base, err := toolchainsDir()
	if err != nil {
		return "", err
	}
	installName := fmt.Sprintf("node-%s-%s-%s", version, osName, archName)
	installDir := filepath.Join(base, installName)
	nodeBin := filepath.Join(installDir, "bin", "node")
	if runtime.GOOS == "windows" {
		nodeBin = filepath.Join(installDir, "node.exe")
	}
	if _, err := os.Stat(nodeBin); err == nil {
		if runtime.GOOS == "windows" {
			return installDir, nil
		}
		return filepath.Join(installDir, "bin"), nil
	}

	archiveName := fmt.Sprintf("node-v%s-%s-%s", version, osName, archName)
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	distURL := fmt.Sprintf("https://nodejs.org/dist/v%s/%s%s", version, archiveName, ext)
	shasumsURL := fmt.Sprintf("https://nodejs.org/dist/v%s/SHASUMS256.txt", version)

	tmpDir, err := os.MkdirTemp(base, "download-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, archiveName+ext)
	if err := downloadAndVerify(distURL, shasumsURL, archiveName+ext, archivePath); err != nil {
		return "", fmt.Errorf("no pude descargar/verificar Node %s: %w", version, err)
	}

	extractedRoot := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(extractedRoot, 0o755); err != nil {
		return "", err
	}
	if ext == ".zip" {
		if err := extractZip(archivePath, extractedRoot); err != nil {
			return "", fmt.Errorf("no pude extraer %s: %w", archivePath, err)
		}
	} else {
		if err := extractTarGz(archivePath, extractedRoot); err != nil {
			return "", fmt.Errorf("no pude extraer %s: %w", archivePath, err)
		}
	}

	// El tarball/zip trae un único directorio top-level (ej.
	// "node-v20.11.0-darwin-arm64/") — se mueve tal cual a installDir en
	// vez de asumir/reconstruir ese nombre, así esto no se rompe si
	// nodejs.org cambia la convención de nombres del contenido interno.
	entries, err := os.ReadDir(extractedRoot)
	if err != nil {
		return "", err
	}
	var topLevel string
	for _, e := range entries {
		if e.IsDir() {
			topLevel = filepath.Join(extractedRoot, e.Name())
			break
		}
	}
	if topLevel == "" {
		return "", fmt.Errorf("el archivo descargado de %s no tiene la forma esperada (sin directorio top-level)", distURL)
	}
	if err := os.Rename(topLevel, installDir); err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		return installDir, nil
	}
	return filepath.Join(installDir, "bin"), nil
}

// downloadAndVerify baja shasumsURL, encuentra la línea que corresponde
// a fileName, baja distURL a destPath, y confirma que su SHA256 coincide
// con el checksum oficial — nunca se usa un archivo cuyo hash no matchea
// exactamente lo que nodejs.org publicó para esa versión.
func downloadAndVerify(distURL, shasumsURL, fileName, destPath string) error {
	client := &http.Client{Timeout: 2 * time.Minute}

	shasumsResp, err := client.Get(shasumsURL)
	if err != nil {
		return fmt.Errorf("no pude bajar %s: %w", shasumsURL, err)
	}
	defer shasumsResp.Body.Close()
	if shasumsResp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s respondió %d — ¿la versión de Node pedida existe de verdad?", shasumsURL, shasumsResp.StatusCode)
	}
	shasumsData, err := io.ReadAll(shasumsResp.Body)
	if err != nil {
		return err
	}

	var expectedSum string
	for _, line := range strings.Split(string(shasumsData), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == fileName {
			expectedSum = fields[0]
			break
		}
	}
	if expectedSum == "" {
		return fmt.Errorf("no encontré %q en %s — el archivo esperado no coincide con lo que nodejs.org publicó", fileName, shasumsURL)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	resp, err := client.Get(distURL)
	if err != nil {
		return fmt.Errorf("no pude bajar %s: %w", distURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s respondió %d", distURL, resp.StatusCode)
	}

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, hasher), resp.Body); err != nil {
		return err
	}
	gotSum := hex.EncodeToString(hasher.Sum(nil))
	if gotSum != expectedSum {
		return fmt.Errorf("checksum de %s no coincide — esperaba %s, obtuve %s (descarga corrupta o interceptada, no se usa)", fileName, expectedSum, gotSum)
	}
	return nil
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		case tar.TypeSymlink:
			// Node ships a couple of symlinks (bin/npm -> ../lib/.../npm-cli.js
			// style shims en algunas versiones) — se recrean tal cual.
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}

func extractZip(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		target, err := safeJoin(destDir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

// safeJoin une destDir con un path que viene DE ADENTRO de un archivo
// descargado (tar/zip) — nunca confiar en que esas entradas ya vienen
// saneadas (un tarball armado a mano podría traer "../../etc/passwd",
// zip-slip es una clase de vulnerabilidad real y conocida). Rechaza
// cualquier entrada que termine fuera de destDir.
func safeJoin(destDir, name string) (string, error) {
	target := filepath.Join(destDir, name)
	if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) && target != filepath.Clean(destDir) {
		return "", fmt.Errorf("entrada de archivo fuera de destino: %q", name)
	}
	return target, nil
}

// EnsurePnpm habilita Corepack (ya viene adentro de Node 16.9+, en el
// mismo bin/ que EnsureNode ya devolvió) para que 'pnpm build' funcione
// sin bajar pnpm aparte — Corepack resuelve/baja la versión de pnpm que
// el propio frontend/package.json del plugin declare en su campo
// "packageManager" (convención ya estándar del ecosistema, no algo que
// Asterion inventa).
func EnsurePnpm(nodeBinDir string) error {
	corepack := filepath.Join(nodeBinDir, "corepack")
	if runtime.GOOS == "windows" {
		corepack = filepath.Join(nodeBinDir, "corepack.cmd")
	}
	if _, err := os.Stat(corepack); err != nil {
		return fmt.Errorf("no encontré corepack en %s — ¿esta versión de Node no lo trae integrado?", nodeBinDir)
	}
	cmd := exec.Command(corepack, "enable", "--install-directory", nodeBinDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("corepack enable falló: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
