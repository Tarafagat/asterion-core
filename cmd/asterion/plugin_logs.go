package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"asterion-core/internal/plugins"
)

func pluginLogsCmd() *cobra.Command {
	var follow bool
	var lines int
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Muestra el log del proceso del plugin (stdout+stderr combinados)",
		Long: "Sin --follow, imprime las últimas líneas del log y termina. Con --follow (-f),\n" +
			"se queda mostrando líneas nuevas en tiempo real a medida que el plugin las\n" +
			"escribe (Ctrl+C para salir) — el uso típico es ver en vivo qué le está\n" +
			"llegando al plugin: la mayoría de los frameworks web logean cada request\n" +
			"entrante a stdout por default (método, path, status).\n\n" +
			"Lee el mismo archivo al que 'asterion plugin start'/'restart' ya redirige\n" +
			"stdout+stderr del proceso — no hace falta que el plugin haga nada especial\n" +
			"para que esto funcione, pero tampoco puede mostrar más de lo que el plugin ya\n" +
			"loguea por su cuenta: si apaga su propio logging de acceso, acá tampoco va a\n" +
			"aparecer.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			installed, err := plugins.Get(args[0])
			if err != nil {
				return err
			}
			path, err := plugins.LogPath(installed)
			if err != nil {
				return err
			}
			if err := printTail(path, lines); err != nil {
				return err
			}
			if follow {
				return followFile(path)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Seguir el log en tiempo real, como 'tail -f' — Ctrl+C para salir")
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "Cuántas líneas finales mostrar antes de (si --follow) seguir en vivo")
	return cmd
}

// printTail imprime las últimas n líneas de un archivo. Lee el archivo
// completo y se queda con la cola — simple y suficiente para un log de
// plugin; no pensado para archivos de gigabytes (ahí haría falta leer
// hacia atrás por chunks en vez de esto).
func printTail(path string, n int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "(el plugin todavía no generó ningún log en %s)\n", path)
			return nil
		}
		return err
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return nil
	}
	allLines := strings.Split(text, "\n")
	start := 0
	if len(allLines) > n {
		start = len(allLines) - n
	}
	for _, line := range allLines[start:] {
		fmt.Println(line)
	}
	return nil
}

// followFile imprime cualquier línea nueva que se agregue al archivo,
// sondeando en vez de inotify/fsevents (evita una dependencia nueva para
// esto). No tolera rotación/truncado a propósito: el log de un plugin lo
// abre siempre Start() en modo append y nunca lo rota ni lo trunca nada de
// Asterion — sumarle esa tolerancia sería resolver un caso que no existe.
func followFile(path string) error {
	var f *os.File
	var err error
	for {
		f, err = os.Open(path)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		// El plugin puede no haber escrito nada todavía — esperar a que
		// el archivo aparezca en vez de fallar de una.
		time.Sleep(300 * time.Millisecond)
	}
	defer f.Close()

	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			fmt.Print(line)
		}
		if err != nil {
			if err != io.EOF {
				return err
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
}
