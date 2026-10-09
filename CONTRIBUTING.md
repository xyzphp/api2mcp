# 贡献指南

欢迎通过 Issue 报告问题或通过 Pull Request 改进 API2MCP。

## 开始开发

1. Fork / 克隆仓库，运行 `make init`。
2. 运行 `make dev`，使用自带 Mock 验证。
3. 提交前执行 `make test`、`go vet ./...`、JS 语法检查和 Docker 构建。
4. 修改界面时检查桌面与 390 px 移动布局；修改配置或行为时同步文档。

具体目录和检查项见[开发文档](docs/development.md)。

## 提交 Issue / PR

提供可复现步骤、预期行为、实际行为和脱敏后的错误。说明操作系统、Docker / Go 版本，以及根路径或子路径部署方式。不要上传真实 `.env`、数据库、Token、Authorization Header 或含凭证的截图。

PR 描述说明问题、改变后的行为和验证方式。新增测试应验证对外行为或真实边界；简单文案与样式调整可用人工验证记录。

沿用项目的原生前端和 Go 结构，优先保持部署简单。兼容性变更、数据库格式调整和凭证优先级变化应先在 Issue 中说明。

提交的代码按项目 [MIT 许可证](LICENSE) 提供。
