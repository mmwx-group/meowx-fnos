# 控制服务 API（网页界面用）

全部在 `http://<NAS>:7899/api/…`，JSON。出错时 HTTP 4xx/5xx + `{"error": "中文原因"}`；
有些操作「做成了但套用失败」时是 200 + `{"…": …, "error": "原因"}`，界面要把 `error` 显示出来。

设置了网页密码（`settings.web.password`）时，除 `GET /api/status`、`POST /api/login` 外都要先登录（Cookie `meowx_session`），没登录回 401。

## 状态与服务

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/status` | `{version, need_login, enabled, profile, core: {running, started_at, error, restarts}, error, mixed_port}`；`need_login` 为真时只有前两项 |
| POST | `/api/login` | `{password}` → 设 Cookie |
| POST | `/api/service` | `{action: "start"\|"stop"\|"restart"}`；start / stop 会改开关并存盘 |
| GET | `/api/settings` | 整份设置（结构见 `agent/internal/store/settings.go`，JSON 键是 snake_case） |
| PUT | `/api/settings` | 整份替换；默认存完立刻套用（`?apply=0` 只存）。回 `{settings, error?}` |
| GET | `/api/runconfig` | 当前在跑的运行配置（YAML 文本，只读看） |
| GET | `/api/logs?after=<序号>&source=core\|agent` | 日志增量：`[{seq, time(ms), source, text}]`，界面每 2 秒拿一次 `after=上次最大 seq` |
| DELETE | `/api/logs` | 清空 |

## 订阅

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/profiles` | `{profiles: [Profile], active: "<id>"}` |
| POST | `/api/profiles` | `{url, name?, use?}` 添加并下载；`use` 或还没有在用的就切过去。回 `{profile, error?}` |
| POST | `/api/profiles/upload?name=&use=1` | 正文是配置文件原文（YAML） |
| PATCH | `/api/profiles/{id}` | `{name?, url?, info_url?, user_agent?}` |
| DELETE | `/api/profiles/{id}` | 在用的不能删（409） |
| POST | `/api/profiles/{id}/update` | 重新下载；在用且在跑时重载。回 `{profile, error?}`（失败时旧副本保留，`profile.error` 也有原因） |
| POST | `/api/profiles/{id}/use` | 切过去（没开的顺手开）；起不来就退回原来的选择 |
| GET | `/api/profiles/{id}/content` | 订阅原文 |

Profile：`{id, name, url?, info_url?, user_agent?, from_panel?, updated_at(秒), update_interval(小时), used, total, expire(秒), error?}`。
`url` 为空 = 本地上传的、不能刷新。`from_panel` = 从主控账户导入的（退出登录时会删）。

## 主控账户（经 `meowx-core panel …`，返回体就是它的 JSON 原样）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/panel` | 登录状态（`panel status`） |
| POST | `/api/panel/login` | `{link}`：主控网页「登录客户端」给的 `miaomiaowu://login?…` 链接 |
| POST | `/api/panel/logout` | 退出；删掉从主控导入的订阅（在用的删掉后停内核） |
| GET | `/api/panel/subscriptions` | 账户下的订阅（`panel subscriptions`） |
| POST | `/api/panel/import` | `{url, name}` 导入账户下的一份并切过去 |
| POST | `/api/panel/po0` | `{action: status\|enable\|disable\|fetch\|add\|remove, address?, token?, index?}` |

## 内核 API（节点、连接、规则、日志）

`/api/core/*` 原样转给内核的外部控制（mihomo REST API，WebSocket 也转），例如：
`GET /api/core/proxies`、`PUT /api/core/proxies/<组>` `{name}`、`GET /api/core/proxies/<节点>/delay?url=…&timeout=5000`、
`GET /api/core/connections`（或 WebSocket）、`DELETE /api/core/connections/<id>`、`GET /api/core/rules`、`PATCH /api/core/configs` `{mode}`。
内核没在跑时回 502 `{"error":"内核没在运行"}`。
