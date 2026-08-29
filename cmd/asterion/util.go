package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

func printJSON(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Println(v)
		return
	}
	fmt.Println(string(data))
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// stdin es el único *bufio.Reader que envuelve os.Stdin en todo el
// proceso. bufio.Reader hace lecturas anticipadas (bufferea más de lo que
// se le pide) — crear uno nuevo por cada prompt pierde silenciosamente
// cualquier byte que el anterior ya haya adelantado del fd real pero
// todavía no haya entregado, así que un segundo prompt encadenado (ej.
// elegir un plugin y después un proyecto, en 'plugin connect' sin
// argumentos) lee vacío. Un único reader compartido evita esa clase de
// bug para cualquier secuencia de prompts, presente o futura.
var stdin = bufio.NewReader(os.Stdin)

// readLine lee una línea completa (con el salto de línea final incluido,
// como bufio.ReadString) del reader compartido de stdin.
func readLine() string {
	line, _ := stdin.ReadString('\n')
	return line
}
