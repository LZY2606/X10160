# 校准追溯站

一个零外部依赖的 Go 本地 Web 服务，用于保存设备原始读数、参考点、带有效期的校准曲线版本和不可变校准结果。

## 构建与运行

```bash
go build ./...
go test ./... -count=1
go run ./cmd/server --addr 127.0.0.1:5234
```

打开 <http://127.0.0.1:5234>，页面标题显示“校准追溯站”。

默认数据文件是 `data/calibration-trace.json`；可使用 `--data /path/to/file.json` 指定。保存采用临时文件原子替换。

## 规则摘要

- 曲线版本为草稿或已发布；发布后不可修改，编辑使用 `expected_revision` 乐观锁。
- 曲线选择只使用：接收时刻之前已确认、且有效期覆盖读数声明采样时刻的已发布版本；选择最新 `confirmed_at`。
- 原始事实与结果分别保存；同设备/样本 id 重复且内容一致为幂等，原始值或事实不同产生冲突，不覆盖历史。
- 分段线性支持多个折线点；多项式限制为一阶或二阶。每段明确声明左右端点是否包含，禁止未声明重叠和嵌套覆盖。
- 总体范围外默认禁止求值；版本显式开启外推时才计算，并把额外不确定度按平方和根合并。
- 单位先校验量纲，再按仿射关系换算；读数的显示/曲线输入换算不会写回原始读数。
- 每个原始读数、曲线、参考点和结果都有 SHA-256 指纹；结果记录曲线指纹、参考点指纹、选择理由、区段和不确定度。
- 两版本历史比较只返回假设值，不创建或改写冻结结果。

## 主要 API

- `POST /api/units`、`POST /api/devices`、`POST /api/references`
- `POST /api/curves`、`PUT /api/curves/{id}`、`POST /api/curves/{id}/publish`、`POST /api/curves/{id}/clone`
- `GET /api/curves/{id}/coverage`
- `POST /api/readings/import`
- `GET /api/trace?device_id=...&sample_id=...`
- `POST /api/compare`
- `POST /api/convert`
- `GET /api/export`、`POST /api/import-data`
