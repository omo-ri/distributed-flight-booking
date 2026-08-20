---
id: D-08
title: gRPC 明文传输
severity: minor
status: todo
phase: backlog
blocks: []
refs:
  - booking-service/internal/grpcclient/flight.go
---

# D-08 gRPC 明文传输

`booking-service/internal/grpcclient/flight.go:39` —— `grpc.WithTransportCredentials(insecure.NewCredentials())`

API Key 以明文在网络上传输，任何能抓包的人都能拿到它然后直接调 flight-service。

**修法**：mTLS。学习价值在于会完整走一遍证书链 —— 自签 CA、签发服务端/客户端证书、SAN 配置、证书轮换。JD 里的"TCP/IP、网络协议分析能力"，TLS 握手是最好的抓包练习对象。
