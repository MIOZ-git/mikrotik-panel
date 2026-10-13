IMAGE_NAME := mikrotik-panel
TAG := armv7
PLATFORM := linux/arm/v7

.PHONY: all build save clean test help

all: build save

build:
	docker buildx build --platform $(PLATFORM) -t $(IMAGE_NAME):$(TAG) --load .

save:
	docker save $(IMAGE_NAME):$(TAG) -o panel.tar
	@ls -lh panel.tar

clean:
	rm -f panel.tar
	docker rmi $(IMAGE_NAME):$(TAG) 2>/dev/null || true

test:
	go vet ./...
	go test ./...

help:
	@echo "Доступные команды:"
	@echo "  make build   — собрать образ"
	@echo "  make save    — сохранить в panel.tar"
	@echo "  make all     — build + save"
	@echo "  make clean   — удалить образ и panel.tar"
	@echo "  make test    — запустить тесты"