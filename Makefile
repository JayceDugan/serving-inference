.PHONY: help up up-litellm up-browser down down-litellm down-browser logs logs-litellm logs-browser ps asr-up asr-down asr-logs asr-test

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

up: ## Start the vLLM inference service
	docker compose up -d

up-browser: ## Start the stack plus the stealth browser (Camoufox)
	# TODO: Update this
	docker compose --profile agent-browser up -d

up-litellm: ## Start vLLM plus the LiteLLM stack (profile: litellm)
	docker compose --profile litellm up -d

down: ## Stop the vLLM inference service
	docker compose down

down-browser: ## Stop and remove the stealth browser container
	docker compose --profile agent-browser down

down-litellm: ## Stop everything including LiteLLM
	docker compose --profile litellm down

logs: ## Follow vLLM logs
	docker compose logs -f vllm_inference

logs-litellm: ## Follow LiteLLM + Postgres logs
	docker compose --profile litellm logs -f litellm litellm_db

logs-browser: ## Follow stealth browser logs
	docker compose --profile lgent-browser logs -f stealthy-browser

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

embeddings-up: ## Start the local ASR stack (profile: asr, pinned to the RTX 5080)
	docker compose -f embeddings/docker-compose.yml up -d --build

embeddings-down: ## Stop and remove the ASR stack
	docker compose -f embeddings/docker-compose.yml down

embeddings-logs: ## Follow ASR stack logs
	docker compose -f embeddings/docker-compose.yml qwen-embeddings logs -f
