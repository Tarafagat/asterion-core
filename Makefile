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

# Recompila e instala en DOS lugares: $GOPATH/bin (vía 'go install', sin
# sudo) y /usr/local/bin (copiado, con sudo si hace falta) — a propósito
# los dos, no uno solo: /usr/local/bin ya está en el $PATH de cualquier
# shell sin que nadie tenga que configurar nada (bug real encontrado en
# vivo: un binario viejo suelto ahí, de antes de usar 'go install', le
# ganaba en el $PATH al nuevo de $GOPATH/bin sin ningún error — server
# quedaba corriendo comandos viejos en silencio). Manteniendo los dos
# lugares siempre idénticos, no importa cuál resuelva tu $PATH primero.
# Único paso que hace falta después de cualquier cambio a este repo: Go
# no tiene "hot reload" para un CLI, el binario instalado queda con el
# código de la última vez que se instaló hasta que se vuelve a correr
# esto.
.PHONY: install
install:
ifeq ($(VERSION),)
	go install ./cmd/asterion
else
	go install -ldflags "-X main.version=$(VERSION)" ./cmd/asterion
endif
	@installed="$$(go env GOPATH)/bin/$(BINARY)"; \
	echo "✓ instalado en $$installed"; \
	if [ -w /usr/local/bin ]; then \
		cp "$$installed" /usr/local/bin/$(BINARY) && copied=1 || copied=0; \
	else \
		sudo cp "$$installed" /usr/local/bin/$(BINARY) && copied=1 || copied=0; \
	fi; \
	if [ "$$copied" = "1" ]; then \
		echo "✓ copiado a /usr/local/bin/$(BINARY) (ya está en el \$$PATH de cualquier shell, sin configurar nada)"; \
		echo "  Si tu shell actual venía arrastrando otro 'asterion' de antes, corré 'hash -r' (bash/zsh)"; \
		echo "  para que olvide la ruta vieja — o abrí una terminal nueva."; \
	else \
		echo "⚠ NO se pudo copiar a /usr/local/bin/$(BINARY) (sudo necesita una terminal interactiva"; \
		echo "  para la contraseña, y este Makefile no la tiene acá) — corré esto a mano:"; \
		echo "    sudo cp $$installed /usr/local/bin/$(BINARY)"; \
	fi; \
	resolved="$$(command -v $(BINARY) 2>/dev/null)"; \
	if [ -n "$$resolved" ] && [ "$$resolved" != "$$installed" ] && [ "$$resolved" != "/usr/local/bin/$(BINARY)" ]; then \
		echo ""; \
		echo "⚠ Además, '$(BINARY)' en tu \$$PATH resuelve a $$resolved — un TERCER binario,"; \
		echo "  distinto de los dos que se acaban de actualizar. Revisá qué es esa ruta."; \
	fi
