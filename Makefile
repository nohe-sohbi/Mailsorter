.PHONY: help build up down logs clean test backend frontend

help:
	@echo "Mailsorter - Makefile commands"
	@echo ""
	@echo "  make build     - Build all Docker images"
	@echo "  make up        - Start all services with Docker Compose"
	@echo "  make down      - Stop all services"
	@echo "  make logs      - Show logs from all services"
	@echo "  make clean     - Remove all containers and volumes"
	@echo "  make test      - Run tests"
	@echo "  make backend   - Build and run backend locally"
	@echo "  make frontend  - Build and run frontend locally"

# Local runs go through compose.local.yml, which publishes the host ports that
# docker-compose.yml deliberately omits for the Dokploy deployment. Without it
# the stack starts and binds nothing, and the URLs printed below are a lie.
COMPOSE = docker compose -f docker-compose.yml -f compose.local.yml

build:
	$(COMPOSE) build

up:
	$(COMPOSE) up -d
	@echo "Services are starting..."
	@echo "Frontend: http://localhost:$${FRONTEND_PORT:-3000}"
	@echo "Backend:  http://localhost:$${BACKEND_PORT:-8080}/health"

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f

clean:
	$(COMPOSE) down -v
	rm -rf frontend/node_modules
	rm -rf frontend/build
	rm -f backend/server
	rm -f backend/mailsorter

test:
	@echo "Running backend tests..."
	cd backend && go test ./...
	@echo "Backend tests completed"

backend:
	@echo "Building backend..."
	cd backend && go build -o server ./cmd/server
	@echo "Starting backend on :8080"
	cd backend && ./server

frontend:
	@echo "Installing frontend dependencies..."
	cd frontend && npm install
	@echo "Starting frontend on :3000"
	cd frontend && npm start
