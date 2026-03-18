.PHONY: run run-mock build frontend frontend-dev tokensync

# 生产模式运行（需配置 SEEINGSTONE_API_TOKEN）
run:
	go run ./cmd/server

# 开发模式运行（使用模拟数据）
run-mock:
	MOCK_MODE=1 go run ./cmd/server

# 编译后端
build:
	go build -o bin/server ./cmd/server

# 前端开发
frontend-dev:
	cd frontend && npm run dev

# 前端构建
frontend:
	cd frontend && npm install && npm run build

# 同步 token 信息：仅搜符合阈值的 token，本地已有则用本地（需 SEEINGSTONE_API_TOKEN）
tokensync:
	go run ./cmd/tokensync
