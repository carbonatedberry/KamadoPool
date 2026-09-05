# ----------------------------------------------------------------------------
# Kamado Pool, top-level Makefile
# ----------------------------------------------------------------------------
.PHONY: help ckpool api api-test ckpool-shell ui ui-dev ui-check up down logs clean

help:
	@echo "Kamado Pool targets:"
	@echo "  make ckpool        Build the ckpool-solo docker image"
	@echo "  make api           Build the kamado-api docker image"
	@echo "  make api-test      Run Go tests for kamado-api (requires Go installed)"
	@echo "  make ui            Build the Svelte UI to ui/dist (requires node)"
	@echo "  make ui-dev        Run the Vite dev server on :5173 with /api proxy"
	@echo "  make ui-check      Run svelte-check type diagnostics"
	@echo "  make ckpool-shell  Open a shell in the built ckpool image"
	@echo "  make up            docker compose up -d --build"
	@echo "  make down          docker compose down"
	@echo "  make logs          Tail ckpool + api logs"
	@echo "  make clean         Remove build artifacts, data, and volumes"

ckpool:
	docker build -t kamado/ckpool:dev ./ckpool

api:
	docker build -t kamado/api:dev -f api/Dockerfile .

api-test:
	cd api && go test ./... -race

ui:
	cd ui && npm install && npm run build

ui-dev:
	cd ui && npm install && npm run dev

ui-check:
	cd ui && npm install && npm run check

ckpool-shell: ckpool
	docker run --rm -it --entrypoint /bin/sh kamado/ckpool:dev

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f

clean:
	docker compose down -v || true
	rm -rf data/ build/ dist/
