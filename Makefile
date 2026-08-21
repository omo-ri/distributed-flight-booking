.PHONY: proto up down test run stop \
        loadtest-seed loadtest-steady loadtest-read-recon loadtest-read-ladder \
        loadtest-write-recon loadtest-write-ladder

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

# ---------------------------------------------------------------------------
# L4 压测（T-06）
#
# 顺序：loadtest-seed → *-recon（闭环摸底，读出 X_max）→ *-ladder（开环突破）
#
# recon 给得出吞吐平台有多高，给不出过载后延迟怎么非线性恶化——闭环下
# VU数 = 吞吐 × 延迟 是恒等式（docs/conventions/testing.md § 7）。两个都要跑。
#
# ladder 的阶梯范围必须来自 recon 的实测值，不能用推算：
#     make loadtest-write-ladder RATE_MAX=<recon 测出的 X_max>
# 不传 RATE_MAX 会退回 design § 1.3 的推算兜底，而那正是待验证的东西。
#
# 容量结论只能来自本机，写进 docs/reports/load/ 并记录机器配置（CLAUDE.md § 4）。
# ---------------------------------------------------------------------------

K6_IMAGE    ?= grafana/k6:0.55.0
K6_BASE_URL ?= http://localhost:8080
# 调阶梯形状用：make loadtest-write-recon K6_EXTRA="-e STEPS=4 -e STEP_DURATION=30s"
# 可调项见 k6/script.js 顶部：STEPS / STEP_DURATION / RAMP_DURATION / PRE_VUS / MAX_VUS
K6_EXTRA    ?=

# 不落 CSV：k6 的 csv 输出按「指标采样」写行而不是按请求写行，实测一个请求 15 行、
# 约 1.76 KB，读路径一次 4 分钟的跑就是 8.4 GB，而其中被用到的不到 2%。
# 现在按档聚合在 k6 内部完成，收尾直接打表并存一份几 KB 的 out/<run>.report.json。
# 需要逐请求的原始数据时再手动加 --out csv=out/x.csv.gz，并用 analyze_ladder.py 看。

# 压测航班不进迁移——它是测试装置，不是系统的一部分。见 k6/loadtest-seed.sql。
loadtest-seed:
	docker compose exec -T flight-db \
		psql -v ON_ERROR_STOP=1 -U flight -d flight_db < k6/loadtest-seed.sql

# $(1)=run 名  $(2)=额外 -e 参数
define run_k6
	@mkdir -p k6/out
	docker run --rm --network host \
		--user "$$(id -u):$$(id -g)" \
		-v "$(PWD)/k6:/scripts" -w /scripts \
		-e BASE_URL=$(K6_BASE_URL) -e RUN_NAME=$(1) $(2) $(K6_EXTRA) \
		$(K6_IMAGE) run script.js
endef

# 平峰稳态：恒定 500 QPS 混合，检查 R2/R3。这个场景有真断言。
loadtest-steady: loadtest-seed
	$(call run_k6,steady,-e SCENARIO=steady -e RATE=$(or $(RATE),500))

loadtest-read-recon: loadtest-seed
	$(call run_k6,read-recon,-e SCENARIO=read -e MODE=recon $(if $(VU_MAX),-e VU_MAX=$(VU_MAX)))

loadtest-read-ladder: loadtest-seed
	$(call run_k6,read-ladder,-e SCENARIO=read -e MODE=ladder $(if $(RATE_MAX),-e RATE_MAX=$(RATE_MAX)))

loadtest-write-recon: loadtest-seed
	$(call run_k6,write-recon,-e SCENARIO=write -e MODE=recon $(if $(VU_MAX),-e VU_MAX=$(VU_MAX)))

loadtest-write-ladder: loadtest-seed
	$(call run_k6,write-ladder,-e SCENARIO=write -e MODE=ladder $(if $(RATE_MAX),-e RATE_MAX=$(RATE_MAX)))
