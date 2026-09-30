# AGENTS.md

## 提交前必须完成

任何代码改动结束前，必须实际运行以下检查，不能只跑局部测试，也不能因为“改动看起来很小”就跳过：

```bash
GOCACHE=/tmp/warpmesh-gocache go test ./...
golangci-lint run ./...   # 本地没有 golangci-lint 时，至少跑 go vet ./...
cd web
npm run build
npx eslint .
npx tsc --noEmit
cd ..
```

- `go test ./...` 是硬性要求。若沙箱拦截 GOCACHE 或本地端口监听，换成可写 `GOCACHE` 或请求本地运行权限后重跑；检查失败不能当作测试通过。
- Vite 的 `npm run build` 不做类型检查，不能替代 `npx tsc --noEmit`。新引入的 TypeScript 错误必须修掉；存量错误要明确报告，不能宣称类型检查通过。
- 改了 `web/src/**` 必须重新 `npm run build`，并提交新的 `web/dist`。server 是 embed `web/dist` 的，禁止旧 dist 配新源码。
- 本地检查通过才允许创建 PR。GitHub Actions 是验收环境，不是“提交前没跑测试”的借口。

## VPS 部署刷新（用户要求做完整交付）

- server 以 systemd `warpmesh.service` 跑在 VPS 上，环境变量在 `/etc/warpmesh.env`，数据目录 `/var/lib/warpmesh`。
- 新 server 二进制替换 `/opt/warpmesh/warpmesh-server` 后执行 `systemctl restart warpmesh.service`，不能只提交不部署。
- 本地设备容器由 Podman 管理：`warpmesh-linux` 是普通 agent，`warpmesh-desktop` 是桌面容器；重建后必须确认设备在 `/api/devices` 中恢复 online。
- 涉及远程桌面的改动，修复后要实际打开一个设备的 remote desktop 页面验证，不能只确认容器 running。

## 容易踩坑的契约

- Go JSON tag 不能随手加 `omitempty`。前端类型声明为必填的字段（如 `deviceIds`）必须始终出现在响应里，空值序列化为 `[]`。
- 改 API handler、struct JSON tag 或前端 API 类型时，必须补或改 API 契约测试，并在最后跑全量 `go test ./...`。
- 前端从 API 拿到的数组/对象一律按可能缺失处理（例如 `deviceIds ?? []`）。渲染路径不能直接对未保护的值调用 `.length`、`.join()`，否则 React 会整棵卸载，界面变成黑屏/白屏。
- 涉及用户、设备、权限、账号页面的改动，至少要本地启动 server 实际点一遍相关页面，确认无空白、黑屏或控制台报错。
- 不要用“构建成功”冒充“测试通过”。报告验证结果时列出实际跑过的命令。
