package dbbackup

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// dumpTimeout iguala el límite que ya usa el lado Python para el modo
// directo (backup_service.DUMP_TIMEOUT_SECONDS). No hay ningún precedente
// de exec.CommandContext con timeout en el resto de este repo — todo
// exec.Command existente corre sin límite de tiempo — así que este es un
// patrón nuevo, deliberado: un mysqldump/pg_dump colgado (conexión que
// nunca cierra, base enorme) no puede bloquear el agente para siempre.
const dumpTimeout = 3600 * time.Second

// Dump corre mysqldump/pg_dump LOCAL — siempre contra 127.0.0.1, el
// agente vive en la misma máquina que la base — y devuelve la ruta a un
// archivo temporal ya comprimido en gzip. El caller es dueño de ese
// archivo y responsable de borrarlo (os.Remove) cuando termine de subirlo.
//
// La salida nunca se junta entera en memoria (a diferencia del lado
// Python, pensado para bases más chicas): cmd.StdoutPipe() se copia
// directo a un gzip.Writer sobre el archivo temporal, así que el pico de
// memoria no depende del tamaño de la base — importa en instancias con
// poca RAM.
func Dump(ctx context.Context, engine string, port int, database, user, password string) (string, error) {
	binary := "mysqldump"
	if engine == "postgres" {
		binary = "pg_dump"
	}
	if _, err := exec.LookPath(binary); err != nil {
		return "", fmt.Errorf(
			"no se encontró el binario %q en esta instancia — instalalo (mysql-client/postgresql-client) para poder hacer backups",
			binary,
		)
	}

	dumpCtx, cancel := context.WithTimeout(ctx, dumpTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if engine == "mysql" {
		cmd = exec.CommandContext(dumpCtx, binary, "-h", "127.0.0.1", "-P", fmt.Sprint(port), "-u", user, database)
		cmd.Env = append(os.Environ(), "MYSQL_PWD="+password)
	} else {
		cmd = exec.CommandContext(dumpCtx, binary, "-h", "127.0.0.1", "-p", fmt.Sprint(port), "-U", user, database)
		cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("no pude abrir stdout de %s: %w", binary, err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	tmpFile, err := os.CreateTemp("", "asterion-dbbackup-*.sql.gz")
	if err != nil {
		return "", fmt.Errorf("no pude crear el archivo temporal para el dump: %w", err)
	}
	defer tmpFile.Close()

	if err := cmd.Start(); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("no pude arrancar %s: %w", binary, err)
	}

	gz := gzip.NewWriter(tmpFile)
	written, copyErr := io.Copy(gz, stdout)
	closeErr := gz.Close()
	waitErr := cmd.Wait()

	if waitErr != nil {
		os.Remove(tmpFile.Name())
		if dumpCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("%s no terminó en %s", binary, dumpTimeout)
		}
		stderrMsg := strings.TrimSpace(stderrBuf.String())
		if stderrMsg == "" {
			stderrMsg = "(sin salida de error)"
		}
		return "", fmt.Errorf("%s terminó con error: %v: %s", binary, waitErr, stderrMsg)
	}
	if copyErr != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("error copiando la salida de %s: %w", binary, copyErr)
	}
	if closeErr != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("error cerrando el gzip: %w", closeErr)
	}
	if written == 0 {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("el dump salió vacío — no se sube nada")
	}

	return tmpFile.Name(), nil
}
