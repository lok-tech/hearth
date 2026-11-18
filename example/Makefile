# Run from the example/ directory.
.PHONY: test site run up down

test:            ## the HTTP-level tests (no database needed)
	go test -race ./...

site:            ## build the Hearth site with the CLI from this checkout
	cd .. && go run ./cmd/hearth build -site example/site

run: site        ## no Docker, no database: the in-memory demo store on http://localhost:8080
	STORE=memory go run .

up:              ## Postgres + app + Caddy (needs Docker)
	docker compose -f compose.yaml up --build

down:
	docker compose -f compose.yaml down -v
