.PHONY: help build run clean test lint

help: ## Показать справку
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Собрать все сервисы
	@echo "Building ReplicatorPublisher..."
	@go build -o bin/publisher cmd/publisher/main.go
	@echo "Building ReplicatorConsumer..."
	@go build -o bin/consumer cmd/consumer/main.go
	@echo "✓ Build completed"

build-publisher: ## Собрать только Publisher
	@echo "Building ReplicatorPublisher..."
	@go build -o bin/publisher cmd/publisher/main.go
	@echo "✓ Publisher built"

build-consumer: ## Собрать только Consumer
	@echo "Building ReplicatorConsumer..."
	@go build -o bin/consumer cmd/consumer/main.go
	@echo "✓ Consumer built"

run-publisher: ## Запустить Publisher
	@echo "Starting ReplicatorPublisher..."
	@./bin/publisher -config config.publisher.yaml

run-consumer: ## Запустить Consumer
	@echo "Starting ReplicatorConsumer..."
	@./bin/consumer -config config.consumer.yaml

dev-publisher: build-publisher run-publisher ## Собрать и запустить Publisher

dev-consumer: build-consumer run-consumer ## Собрать и запустить Consumer

clean: ## Очистить сборку
	@echo "Cleaning..."
	@rm -rf bin/
	@echo "✓ Clean completed"

test: ## Запустить тесты
	@echo "Running tests..."
	@go test -v ./...

lint: ## Проверить код линтером
	@echo "Running linter..."
	@golangci-lint run ./...

mod-tidy: ## Обновить зависимости
	@echo "Tidying go.mod..."
	@go mod tidy
	@echo "✓ Dependencies updated"

mod-download: ## Скачать зависимости
	@echo "Downloading dependencies..."
	@go mod download
	@echo "✓ Dependencies downloaded"

fmt: ## Форматировать код
	@echo "Formatting code..."
	@go fmt ./...
	@echo "✓ Code formatted"

.DEFAULT_GOAL := help

