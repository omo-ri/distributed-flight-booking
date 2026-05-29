.PHONY: proto up down test run stop

proto:
	protoc \
		--proto_path=proto \
		--go_out=flight-service/pb --go_opt=paths=source_relative \
		--go-grpc_out=flight-service/pb --go-grpc_opt=paths=source_relative \
		proto/flight/flight.proto

up:
	docker compose up --build -d

down:
	docker compose down -v

# 安装 pytest 依赖
deps:
	pip install -r tests/requirements.txt

# 一键启动 + 跑测试
run: deps
	docker compose up --build -d
	@echo "⏳ Waiting for services to be ready..."
	@for i in $$(seq 1 60); do \
		curl -sf http://localhost:8080/metrics > /dev/null && \
		curl -sf http://localhost:9091/metrics > /dev/null && break; \
		sleep 1; \
	done
	pytest tests/ -v

# 一键停止并清理
stop:
	docker compose down -v

# 仅跑测试（服务已启动时）
test: deps
	pytest tests/ -v

simple-up:
	docker compose up -d
