# VPS Monitor —— 开发与构建入口
#
# 约定：所有 Go 命令都在 go.work 工作区下跑，因此要显式列出三个模块的包路径
# （工作区根目录没有 go.mod，`go build ./...` 用不了）。

SHELL := /bin/sh

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOPKGS  := ./proto/... ./agent/... ./server/...

DIST        := dist
AGENT_DIST  := $(DIST)/agent
SERVER_DIST := $(DIST)/server
EMBED_DIST  := server/web/dist

.PHONY: help dev-server dev-web build build-agent build-web build-server lint test tidy clean

help: ## 列出可用目标
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

dev-server: ## 本地跑服务端（:9000）
	go run ./server/cmd/server

dev-web: ## 本地跑前端（:8080，/api 代理到 :9000）
	cd web && npm run dev

build: build-agent build-server ## 构建 agent 与服务端

build-agent: ## 交叉编译 agent 到 dist/agent/（linux amd64 + arm64）
	@mkdir -p $(AGENT_DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" \
		-o $(AGENT_DIST)/vps-agent-linux-amd64 ./agent/cmd/agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" \
		-o $(AGENT_DIST)/vps-agent-linux-arm64 ./agent/cmd/agent

build-web: ## 构建前端并放到 server/web/dist 供 go:embed 取用
	cd web && npm run build
	rm -rf $(EMBED_DIST)
	mkdir -p $(EMBED_DIST)
	cp -r web/dist/. $(EMBED_DIST)/

build-server: build-web ## 构建内嵌前端的服务端二进制
	@mkdir -p $(SERVER_DIST)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" \
		-o $(SERVER_DIST)/vps-server ./server/cmd/server

lint: ## Go vet + 前端 tsc/eslint
	go vet $(GOPKGS)
	cd web && npm run tsc:check && npm run lint

test: ## Go 单测
	go test $(GOPKGS)

tidy: ## 整理三个模块的依赖
	cd proto  && go mod tidy
	cd agent  && go mod tidy
	cd server && go mod tidy

clean: ## 清理构建产物
	rm -rf $(DIST) web/dist
