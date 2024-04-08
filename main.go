package main

import (
	"os"
	"simple_pbft/pbft/consensus"
	"simple_pbft/pbft/network"
	"strconv"
)

func main() {
	genRsaKeys("N")
	genRsaKeys("M")
	genRsaKeys("P")
	nodeID := os.Args[1]
	clusterName := os.Args[2]
	nodeNumStr := os.Args[3]
	// 将字符串转换为整数
	nodeNum, _ := strconv.Atoi(nodeNumStr)
	consensus.F = nodeNum
	server := network.NewServer(nodeID, clusterName)

	server.Start()

}
