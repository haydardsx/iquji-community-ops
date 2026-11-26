# 第三方组件声明

[本版本的许可](LICENSE)仅覆盖权利人获准按该许可提供的自有内容。已按[旧许可](licenses/IQuji-Ops-1.0.txt)取得的软件继续适用原条款；第三方内容及其独立授权完整保留。分发二进制、镜像或修改后的组件时，仍须遵守对应版本的版权、许可、通知及适用的源码提供义务。

当前锁定版本的 Community Web 包按[随包 MIT 许可](licenses/Community-MIT.txt)提供；该声明只适用于这些组件。Ops 自有代码适用本版本根目录的许可。

| 组件范围 | 来源与许可依据 |
| --- | --- |
| 共享 Web 控件、主题、图标、网络等 | 依赖锁文件固定的 [Community Web](https://github.com/haydardsx/iquji-community-web) npm 制品采用 MIT，分发时保留各包版权及 LICENSE |
| Web 直接与传递依赖 | 版本由 `client/package.json`、`client/bun.lock` 确定；许可采用各发布包自带 LICENSE/NOTICE |
| 交互终端 | `@xterm/xterm` 与 `@xterm/addon-fit` 采用 MIT，桌面包的 `client/third-party-notices.json` 按锁定版本收录各自许可。终端样式随 `@iquji-community/web-component` 提供，样式的上游版权与许可声明随该组件包一并收录 |
| Go API/Worker 依赖 | 版本由 `server/go.mod`、`server/go.sum` 确定；许可采用各 Go 模块附带声明 |
| Tauri、Rust 直接与传递依赖 | 版本由 `desktop/Cargo.toml`、`desktop/Cargo.lock` 确定；许可采用对应 crate 附带声明 |
| frpc | [FRP v0.69.1](https://github.com/fatedier/frp/releases/tag/v0.69.1)，采用 Apache-2.0；桌面包的 `runtime/frpc-LICENSE` 保留随上游归档发布的许可证 |
| 托管 Jenkins 插件 | 桌面包在 `resources/runtime/managed/jenkins/plugins.lock` 列出各插件的版本与 SHA-256。`jenkins-plugins.tar` 保留相应的上游发行文件，包括插件和内嵌依赖自带的声明。`runtime/third-party-notices.json` 按插件列出许可正文、版权声明及取得相应版本源码的地址。上游制品来自 [Jenkins 插件归档](https://archives.jenkins.io/plugins/) |
| Jenkins、BuildKit、Traefik、数据库等远端运行组件 | 各组件或镜像来自其官方公开项目；安装或再分发时保留上游许可。它们不因由 OPS 管理而转为 OPS 许可 |
| 前后端 DEMO | 独立示例仓库 `demo_deploy_backend`、`demo_deploy_frontend` 各采用 MIT，具体版权声明见各自 LICENSE |

桌面发行包内的 `client/third-party-notices.json` 收录已打包的 Web 组件，`runtime/third-party-notices.json` 收录桌面程序和随包二进制的原生组件。两份清单均按组件及版本列出对应许可正文和版权声明。
