# 字节流重组

支持 32 位序号回绕比较、乱序与重叠重组（重叠处认先确认的字节）、
重复分片丢弃、缺洞时只交付连续前缀，缓存占用有上限，顶满时扔最老的
片段。拉起环境：

```
docker compose up --build --abort-on-container-exit
```
