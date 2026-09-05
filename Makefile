.PHONY: help up up-browser down down-browser logs logs-browser ps asr-up asr-down asr-logs asr-test

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

up: ## Start Open WebUI
	docker compose up -d

up-browser: ## Start the stack plus the stealth browser (Camoufox)
	docker compose --profile agent-browser up -d

down: ## Stop the stack
	docker compose down

down-browser: ## Stop and remove the stealth browser container
	docker compose --profile agent-browser down

logs: ## Follow Open WebUI logs
	docker compose logs -f openwebui

logs-browser: ## Follow stealth browser logs
	docker compose --profile agent-browser logs -f stealthy-browser

ps: ## Show running services
	docker compose ps

asr-up: ## Start the local ASR stack (profile: asr, pinned to the RTX 5080)
	docker compose -f asr/docker-compose.yml up -d --build

asr-down: ## Stop and remove the ASR stack
	docker compose -f asr/docker-compose.yml down

asr-logs: ## Follow ASR stack logs
	docker compose -f asr/docker-compose.yml logs -f

asr-test: ## Run the asr-api Go test suite
	cd asr/api && go test ./...
