# session-gateway

代理模型请求，把对话按会话存成 JSONL；同时提供一个只读 MCP 工具，
让 agent 自己在存档上跑 `grep` / `sed` / `jq`。

不做关键词检索、不做摘要、不做数据库——存档就是文件，检索交给 shell。

## 部署

```bash
cp config.example.yaml config.yaml
$EDITOR config.yaml          # 改 key-list、proxy-list
mkdir -p data/session && sudo chown -R 1002:1002 data/session   # 换成你的 UID/GID

PUID=1002 PGID=1002 docker compose up -d
```

`PUID`/`PGID` 必须和宿主机上 `data/session` 的属主一致，否则写不进去。

想让 agent 只能读、不能改存档，再加一个 `BASH_UID`（任意未占用的 uid）：

```bash
PUID=1002 PGID=1002 BASH_UID=1003 docker compose up -d
```

此时 `session_bash` 以 `BASH_UID` 运行，`rm` / `>` 之类会被内核拒绝。

## 配置

```yaml
listen: ":8080"
data-dir: "/data/session"
key-list:
  - "mg_..."
proxy-list:
  - path: "anthropic"
    origin: "https://api.anthropic.com"
```

客户端把 base_url 指向 `http://<nas>:8080/anthropic`，请求会转发到上游，
同时把这一轮的提问与答复追加进 `/data/session/<会话>.jsonl`。

## MCP

客户端配置：

```json
{
  "mcpServers": {
    "session-gateway": {
      "type": "http",
      "url": "http://172.17.0.1:8080/mcp",
      "headers": {
        "Authorization": "Bearer mg_..."
      }
    }
  }
}
```

`172.17.0.1` 是 Docker 默认网桥的网关地址——MCP 客户端跑在宿主机或
另一个容器里时，用它访问 `session-gateway`。部署在别的机器上就换成那台机器的 IP。

两个工具：

| 工具 | 用途 |
|---|---|
| `session_bash` | 在存档目录执行 bash，可用管道与重定向（能否写入取决于 `BASH_UID`） |
| `session_list` | 列出会话：文件名、首条提问、行数、时间 |

## 存档格式

一个会话一个 `.jsonl`，每行一条消息：

```json
{"ts":"2026-11-08T14:31:06Z","role":"user","content":"投屏时报 403","model":"m","session_id":"abc","path":"anthropic"}
{"ts":"2026-11-08T14:31:06Z","role":"assistant","content":"投屏握手报 403，是签名错误","session_id":"abc","path":"anthropic"}
```

JSONL 的好处是 `grep` 能按行工作：搜到的就是一行，而不是一大段文本。

```bash
grep -rn "403" /data/session/          # 哪些会话提过
grep -n "403" abc-1234abcd.jsonl       # 定位到行号
sed -n '120,160p' abc-1234abcd.jsonl   # 读那一段
jq -r 'select(.role=="user") | .content' abc-1234abcd.jsonl
```

## 开发

```bash
go test ./...
go run ./cmd/session-gateway      # 读 ./config.yaml
```
