.PHONY: help up up-litellm up-browser down down-litellm down-browser logs logs-litellm logs-browser ps

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

up: ## Start the vLLM inference service
	docker compose up -d

up-browser: ## Start the stack plus the stealth browser (Camoufox)
	docker compose up -d stealthy-browser

up-litellm: ## Start vLLM plus the LiteLLM stack (profile: litellm)
	docker compose --profile litellm up -d

down: ## Stop the vLLM inference service
	docker compose down

down-browser: ## Stop and remove the stealth browser container
	docker compose stop stealthy-browser && docker compose rm -f stealthy-browser

down-litellm: ## Stop everything including LiteLLM
	docker compose --profile litellm down

logs: ## Follow vLLM logs
	docker compose logs -f vllm_inference

logs-litellm: ## Follow LiteLLM + Postgres logs
	docker compose --profile litellm logs -f litellm litellm_db

logs-browser: ## Follow stealth browser logs
	docker compose logs -f stealthy-browser

ps: ## Show running services
	docker compose ps
