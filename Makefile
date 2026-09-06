.PHONY: help up down logs ps up-browser down-browser logs-browser asr-up asr-down asr-logs asr-test embed-up embed-down embed-logs embed-test

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

up: ## Start Open WebUI
	docker compose up -d

down: ## Stop the whole stack (all services, incl. profiled ones)
	docker compose --profile unsloth --profile embedding --profile agent-browser --profile asr down

logs: ## Follow Open WebUI logs
	docker compose logs -f openwebui

ps: ## Show running services
	docker compose ps

up-browser: ## Start the stack plus the stealth browser (Camoufox)
	docker compose --profile agent-browser up -d

down-browser: ## Stop and remove only the stealth browser container
	docker compose down stealthy-browser

logs-browser: ## Follow stealth browser logs
	docker compose logs -f stealthy-browser

asr-up: ## Start the ASR stack (profile: asr, pinned to the RTX 5080)
	docker compose --profile asr up -d --build

asr-down: ## Stop and remove only the ASR containers
	docker compose down asr-model cleanup-model asr-api

asr-logs: ## Follow ASR stack logs
	docker compose logs -f asr-model cleanup-model asr-api

asr-test: ## Run the asr-api Go test suite
	cd services/asr/api && go test ./...

embed-up: ## Start the ONNX embedding service (profile: embedding)
	docker compose --profile embedding up -d --build embeddings_model

embed-down: ## Stop and remove the embedding service
	docker compose down embeddings_model

embed-logs: ## Follow embedding logs
	docker compose logs -f embeddings_model

embed-test: ## Check the embedding service health endpoint
	curl -fsS http://127.0.0.1:8020/health && echo
