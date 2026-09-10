package dbbackup

import (
	"context"
	"fmt"
	"net/http"
	"os"
)

// UploadContentType tiene que coincidir EXACTO con el content_type que
// Cloud usó al firmar la URL (ver
// firebase_storage_service.generate_upload_url del otro lado) — Google
// Cloud Storage rechaza la firma si no matchea.
const UploadContentType = "application/gzip"

// Upload sube el archivo ya comprimido directo a la URL firmada que Cloud
// generó — un PUT plano a Firebase Storage, nunca proxeado por la API de
// Cloud.
func Upload(ctx context.Context, uploadURL, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("no pude abrir el dump para subirlo: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("no pude leer el tamaño del dump: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, file)
	if err != nil {
		return fmt.Errorf("no pude armar la subida: %w", err)
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", UploadContentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("la subida a Firebase Storage falló: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("Firebase Storage respondió %d al subir el dump", resp.StatusCode)
	}
	return nil
}
