package main

import "testing"

func TestParseConfig(t *testing.T) {
	cfg, err := parseConfig(nil, "19444")
	if err != nil || cfg.addr != "127.0.0.1:19444" {
		t.Fatalf("PORT 未正确转换为回环地址：%+v %v", cfg, err)
	}
	if _, err = parseConfig([]string{"-addr=0.0.0.0:19081"}, ""); err == nil {
		t.Fatal("应拒绝非回环监听地址")
	}
	if _, err = parseConfig([]string{"-addr=127.0.0.1:8080"}, ""); err != nil {
		t.Fatalf("显式指定端口应允许：%v", err)
	}
}
