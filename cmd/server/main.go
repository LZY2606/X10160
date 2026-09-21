package main

import (
	"flag"
	"log"
	"net/http"

	"calstation/internal/station"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:5234", "HTTP 监听地址")
	data := flag.String("data", "data/station.json", "数据文件路径（空字符串表示仅内存）")
	seed := flag.Bool("seed", false, "启动时若库为空则写入演示数据")
	flag.Parse()

	store, err := station.Open(*data)
	if err != nil {
		log.Fatalf("打开数据失败: %v", err)
	}
	if *seed {
		if _, err := store.SeedDemo(); err != nil {
			log.Fatalf("演示数据写入失败: %v", err)
		}
	}
	srv := station.NewServer(store)
	log.Printf("校准追溯站已启动: http://%s", *addr)
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
