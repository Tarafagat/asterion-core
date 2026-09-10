package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// agentAPIRequest hace una llamada autenticada con X-Asterion-Api-Key
// contra la API de Cloud — reemplaza el boilerplate que reportHeartbeat,
// reportOnce y postJobResult repetían cada uno por su cuenta (mismo
// request, mismo header, mismo manejo de status code, casi calcado 3
// veces). Devuelve el body crudo de la respuesta: cada caller decide cómo
// interpretarlo — reportHeartbeat, por ejemplo, tolera un body vacío o
// no-JSON como "sin jobs" en vez de tratarlo como error (ver su propio
// comentario), así que ese criterio queda ahí, no acá.
//
// body puede ser nil (sin cuerpo, ej. un GET). Un status >= 300 siempre es
// error, con el body de la respuesta incluido en el mensaje cuando lo hay
// (los "detail" de FastAPI viajan ahí).
func agentAPIRequest(ctx context.Context, apiBaseURL, apiKey, method, path string, body any) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, apiBaseURL+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Asterion-Api-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("la API respondió %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}
