# Build helpers para el CLI de Asterion. Un 'go build' plano sigue
# funcionando (queda "asterion version dev") — este Makefile es solo para
# que el binario real refleje la versión del último commit, sin tener que
# escribirla a mano en ningún lado ni mantener un archivo VERSION aparte.

BINARY  := asterion
# Saca "vX.Y" (o "vX.Y.Z") del prefijo "VX.Y ..." del mensaje del último
# commit — la convención informal de versionado que ya se usa en los
# commits de este repo y del resto del ecosistema (no hay releases
# etiquetados en git todavía, ver CHANGELOG.md). Tolera tanto "V0.9" como
# el typo histórico "V.0.3". Si el commit no arranca con esa forma,
# VERSION queda vacío y el build cae a "dev" en vez de fallar.
VERSION := $(shell git log -1 --format=%s 2>/dev/null | grep -oE '^V\.?[0-9]+(\.[0-9]+)*' | sed -E 's/^V\.?/v/')

.PHONY: build
build:
ifeq ($(VERSION),)
	go build -o $(BINARY) ./cmd/asterion
else
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/asterion
endif

.PHONY: version
version:
	@echo $(if $(VERSION),$(VERSION),dev)

# Recompila e instala en $GOPATH/bin (mismo lugar que 'go install' de
# siempre) — para actualizar el binario real que corrés desde cualquier
# lado sin tener que copiarlo a mano. Único paso que hace falta después
# de cualquier cambio a este repo: Go no tiene "hot reload" para un CLI,
# el binario instalado queda con el código de la última vez que se
# instaló hasta que se vuelve a correr esto.
.PHONY: install
install:
ifeq ($(VERSION),)
	go install ./cmd/asterion
else
	go install -ldflags "-X main.version=$(VERSION)" ./cmd/asterion
endif
	@echo "✓ instalado en $$(go env GOPATH)/bin/$(BINARY)"
