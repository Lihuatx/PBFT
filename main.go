package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"simple_pbft/pbft/consensus"
	"simple_pbft/pbft/network"
	"strconv"
	"time"
)

type PerformanceMetrics struct {
	Timestamp    string
	HeapAlloc    uint64
	HeapInuse    uint64
	StackInUse   uint64
	NumGoroutine int
	TotalSys     uint64 // 改用 Sys 来表示总内存
}

func monitorPerformance(nodeID string) {
	// 创建 performance_data 目录
	if err := os.MkdirAll("performance_data", 0755); err != nil {
		fmt.Printf("Error creating performance_data directory: %v\n", err)
		return
	}

	// 直接在 performance_data 目录下创建文件
	filename := filepath.Join("performance_data", fmt.Sprintf("%s_performance.csv", nodeID))
	file, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Error creating file: %v\n", err)
		return
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	writer.Write([]string{"Timestamp", "NodeID", "HeapAlloc(MB)", "HeapInuse(MB)", "StackInUse(MB)", "NumGoroutine", "TotalAlloc(MB)"})

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var m runtime.MemStats
	startTime := time.Now()

	fmt.Printf("Started monitoring performance for node %s\n", nodeID)
	fmt.Println("Timestamp | NodeID | HeapAlloc(MB) | HeapInuse(MB) | StackInUse(MB) | NumGoroutine | TotalAlloc(MB)")
	fmt.Println("-----------------------------------------------------------------------------------------")

	for range ticker.C {
		if time.Since(startTime) >= 60*time.Second {
			fmt.Printf("Monitoring completed for node %s after 60 seconds\n", nodeID)
			break
		}

		runtime.ReadMemStats(&m)
		metrics := PerformanceMetrics{
			Timestamp:    time.Now().Format("2006-01-02 15:04:05"),
			HeapAlloc:    m.HeapAlloc,
			HeapInuse:    m.HeapInuse,
			StackInUse:   m.StackSys,
			NumGoroutine: runtime.NumGoroutine(),
			TotalSys:     m.Sys, // 使用 Sys 而不是 TotalAlloc
		}

		heapAllocMB := float64(metrics.HeapAlloc) / 1024 / 1024
		heapInuseMB := float64(metrics.HeapInuse) / 1024 / 1024
		stackInUseMB := float64(metrics.StackInUse) / 1024 / 1024
		totalSysMB := float64(metrics.TotalSys) / 1024 / 1024 // 总系统内存

		// 打印格式修改
		fmt.Printf("%s | %6s | %11.2f MB | %12.2f MB | %13.2f MB | %11d | %12.2f MB (Total Sys)\n",
			metrics.Timestamp,
			nodeID,
			heapAllocMB,
			heapInuseMB,
			stackInUseMB,
			metrics.NumGoroutine,
			totalSysMB,
		)

		writer.Write([]string{
			metrics.Timestamp,
			nodeID,
			fmt.Sprintf("%.2f", heapAllocMB),
			fmt.Sprintf("%.2f", heapInuseMB),
			fmt.Sprintf("%.2f", stackInUseMB),
			strconv.Itoa(metrics.NumGoroutine),
			fmt.Sprintf("%.2f", totalSysMB),
		})
		writer.Flush()
	}
}

// 需要输入的参数，nodeID ClusterName ClusterNodeNum ClusterNum
func main() {
	genRsaKeys("N")
	genRsaKeys("M")
	genRsaKeys("P")
	genRsaKeys("J")
	genRsaKeys("K")
	nodeID := os.Args[1]
	clusterName := os.Args[2]
	sendMsgNumber := 1
	if nodeID == "client" {
		client := network.ClientStart(clusterName)

		go client.SendMsg(sendMsgNumber)

		client.Start()
	} else {
		nodeNumStr := os.Args[4]
		// 将字符串转换为整数
		nodeNum, _ := strconv.Atoi(nodeNumStr)
		consensus.F = (nodeNum - 1) / 3
		network.ClusterNumber, _ = strconv.Atoi(os.Args[3])
		// 设置默认值
		// 检查是否提供了第5个参数
		if len(os.Args) > 5 { // 判断节点是正常节点还是恶意节点
			network.IsMaliciousNode = os.Args[5] // 使用提供的第三个参数
			//fmt.Println("get args 5")
		}

		//监测内存使用情况
		go monitorPerformance(nodeID)

		server := network.NewServer(nodeID, clusterName)

		server.Start()
	}

}
