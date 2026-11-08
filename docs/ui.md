# 网页管理界面

`agent/web/`，经 `go:embed web` 嵌进 meowx-agent，端口 7899。没有构建步骤、不连 CDN：纯 HTML + CSS + 原生 ES 模块，
框架是随包带的 Preact + htm 单文件发布版（`vendor/preact-htm.js`，即 htm 3.1.1 的 `preact/standalone.module.js`，
Preact MIT / htm Apache-2.0，许可证在 `vendor/`）。改了网页要重新编 agent。

## 文件

| 文件 | 内容 |
|---|---|
| `index.html` / `app.css` | 外壳与全部样式（品牌 token 照 OpenWrt 版 meowx.css / 客户端 Theme.swift，跟随系统深浅色） |
| `js/app.js` | 顶栏、页签（`#锚点` 记当前页）、登录框、全局横条（连不上控制服务、透明代理试用倒计时 +「保留设置」） |
| `js/api.js` | 请求封装：出错抛 `ApiError`（`message` = 回包的 `error` / `message`），401 切到登录框 |
| `js/store.js` | 几页共用的状态：运行状态（每 3 秒，含 `GET /api/proxy` 的试用状态）、订阅列表、主控账户、提示条 |
| `js/ui.js` | 元件：卡片、表单行、开关、分段、按钮（返回 Promise 时自动转圈）、列表输入、弹窗、用量条、设置表单 |
| `js/pages/*.js` | 十个页签：概览 / 订阅 / 节点 / 混入设置 / 透明代理 / 喵窝 / 连接 / 日志 / 辅助功能（「加白」一组：po0、GGY、AkileCDN 白名单）/ 设置 |

## 设置怎么存

`PUT /api/settings` 是整份替换，几页各管一部分（混入设置 = `mixin`、透明代理 = `proxy`、设置页 = 自动更新间隔 / 检查配置、
概览的出站模式 = `mixin.mode`、网页密码 = `web.password`）。表单（`useSettingsForm`）载入时记一份原样，保存时先取最新整份再
三方合并：这页没动过的字段用服务端最新的，动过的用表单里的——别的页签同时改的（例如概览里切了出站模式）不会被这页的旧值盖回去。
页面切走再切回时，没改过的表单重新载入；有没保存的修改时关页会提醒。混入设置 / 透明代理保存即套用（重启内核），
设置页与出站模式只存（`?apply=0`）。

## 本地预览

```sh
cd ../meowx-core && go build -o /tmp/meowx-core-darwin .        # 依赖下载去掉代理环境变量、GOPROXY=https://proxy.golang.org,direct
cd agent && MEOWX_DATA=/tmp/meowx-ui-data MEOWX_CORE=/tmp/meowx-core-darwin go run .
```

浏览器开 http://localhost:7899 。Mac 上旁路由 / TUN 不会真的生效（旁路由回「只在 Linux 上可用」）。AkileCDN 白名单联调时让 meowx-core 带着
`MEOWX_AKILE_API=<本机假接口>`（别拿真 Key 对着 cdnapi.akile.ai 试，PUT 会真的改掉白名单）。
