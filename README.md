# EpicAI

**OpenAI 兼容接口模拟器 / 故障注入平台 / 人工会话接管平台**

EpicAI 不运行任何真实大模型，而是完整复刻 OpenAI API 的协议行为，用于调试 AI 客户端、Agent、网关、SDK、重试组件与前端聊天程序。它可以在毫秒级启动、无成本地产生**可控、可复现**的流式响应、错误与网络异常。

---
直接调用：
```
curl -N http://epicai.adomain.eu.org/v1/responses \
  -H "Authorization: Bearer test" \
  -H "Content-Type: application/json" \
  -d '{"model":"epic-alpha","input":"Responses Test","stream":true}'
```
---

## 核心能力

### 1. 接口模拟器

- 兼容 `POST /v1/chat/completions`、`POST /v1/responses`、`GET /v1/models`、`/v1/files`
- 完整 SSE 流式分帧，输出格式与 OpenAI 一致，客户端无需改动即可接入
- 内置 **Infinite Echo**：将用户输入无限回显，直到被中断，用于长连接稳定性验证
- 多模态输入（文本 / 图片 / 文件引用）与文件、资源管理
- 内置模型 `epic-alpha`，可在后台动态增删模型并配置行为

### 2. 调试与故障注入

后台可视化面板（`/admin/`）支持对**运行中的会话实时操作**，无需重启：

| 能力 | 说明 |
|---|---|
| 会话接管 | `takeover` 人工接管，手动发送消息，再 `return` 回自动回显 |
| 错误注入 | 随时注入 400/401/404/429/500/502/503/504 等错误，支持自定义 `code`/`message` |
| 错误模式 | SSE error 事件、直接断开连接、流中 HTTP 错误、畸形分块 |
| 自定义响应 | 静态响应、自定义错误体、原始 JSON 原样返回 |
| 限速/节奏 | 动态调整 token 速率、回显间隔、分块大小、回显内容模式 |
| 审计日志 | 全链路 TraceID 审计日志，控制台可查看每个请求的完整流程 |

预置错误模板覆盖 400 → 504 常见状态码，用于验证客户端重试、退避、降级逻辑。

### 3. 压测与性能评估

- **内置基准测试**：通过真实 Echo 引擎 + 分词器 + SSE 序列化路径测量本机可持续输出吞吐，返回平均/峰值 token 速率、CPU、内存
  - 后台接口：`POST /admin/api/benchmarks`（`protocol` / `duration_ms` / `chunk_size`）
- **全局限速与配额**：全局 / 单会话 token 速率、burst 窗口，用于模拟限流场景
- **资源保护**：最大并发会话数、单 IP / 单 Key 会话上限、最大文件与资源体积、单会话事件数上限
- **过载策略**：`reject` / `queue` / `hang` / `custom_error`，可验证网关在压力下的行为
- **长连接压测**：Infinite Echo 可持续输出，配合 `max_echo_rate` 与保留天数控制资源

---

## 快速开始

### 直接运行二进制

从 [Releases](https://github.com/CangShui/EpicAI/releases) 下载：

```bash
# Linux
chmod +x epicai-linux-amd64 && ./epicai-linux-amd64

# Windows
epicai-windows-amd64.exe
```

默认监听 `0.0.0.0:8000`：

- 管理后台：http://localhost:8000/admin/
- 健康检查：http://localhost:8000/health
- OpenAI 接口：http://localhost:8000/v1/

默认管理员账号 `admin` / `epicai`（**仅供开发，生产请修改**）。

### Docker

```bash
docker compose up -d
```

### 从源码构建

```bash
cd backend
go build -o ../epicai ./cmd/epicai
```

---

## 使用方法

### 调用模拟接口

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "epic-alpha",
    "stream": true,
    "messages": [{"role": "user", "content": "hello"}]
  }'
```

服务会持续流式回显 `hello`，直到客户端断开。

### 后台调试会话

1. 打开 `http://localhost:8000/admin/` 并登录
2. 在 **Sessions** 中找到目标会话，即可暂停 / 接管 / 注入错误 / 调整速率
3. 在 **Models** 中配置模型行为（回显间隔、错误状态码、是否启用 Agent 等）
4. 在 **Logs / Audit** 中按 TraceID 追踪完整请求链路

### 运行验收测试

```bash
pip install openai
python run_all_tests.py
```

覆盖 T001–T162 全部验收项与核心综合测试。

---

## 配置

通过环境变量或 `.env` 覆盖：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `EPICAI_HOST` | `0.0.0.0` | 监听地址 |
| `EPICAI_PORT` | `8000` | 监听端口 |
| `EPICAI_ADMIN_USER` | `admin` | 管理员账号 |
| `EPICAI_ADMIN_PASSWORD` | `epicai` | 管理员密码 |
| `EPICAI_DATABASE_URL` | `sqlite:data/epicai.db` | 数据库 |
| `EPICAI_STORAGE_PATH` | `data/storage` | 资源存储路径 |
| `EPICAI_DEFAULT_MODEL` | `epic-alpha` | 默认模型 |
| `EPICAI_ALLOW_ANY_KEY` | `true` | 是否允许任意 API Key |
| `EPICAI_LOG_LEVEL` | `info` | 日志级别 |

---

## 适用场景

- AI 客户端 / SDK 的流式协议兼容性验证
- 重试、退避、熔断、降级逻辑调试（可复现的 429 / 5xx / 断连）
- 网关与中间件在高并发、限流、超时下的行为验证
- 长连接稳定性与内存占用压测
- 无需真实模型即可离线开发的 Mock 后端

## 许可证

本项目仅供测试与调试用途。
