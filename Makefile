.PHONY: help up up-litellm down down-litellm logs logs-litellm ps

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

up: ## Start the vLLM inference service
	docker compose up -d

up-litellm: ## Start vLLM plus the LiteLLM stack (profile: litellm)
	docker compose --profile litellm up -d

down: ## Stop the vLLM inference service
	docker compose down

down-litellm: ## Stop everything including LiteLLM
	docker compose --profile litellm down

logs: ## Follow vLLM logs
	docker compose logs -f vllm_inference

logs-litellm: ## Follow LiteLLM + Postgres logs
	docker compose --profile litellm logs -f litellm litellm_db

ps: ## Show running services
	docker compose ps
