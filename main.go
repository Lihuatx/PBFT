package main

import (
	"os"
	"simple_pbft/pbft/consensus"
	"simple_pbft/pbft/network"
	"strconv"
)

// 需要输入的参数，nodeID ClusterName ClusterNodeNum ClusterNum
func main() {
	genRsaKeys("N")
	genRsaKeys("M")
	genRsaKeys("P")
	genRsaKeys("J")
	genRsaKeys("K")
	nodeID := os.Args[1]
	clusterName := os.Args[2]
	sendMsgNumber := 500
	if nodeID == "client" {
		client := network.ClientStart(clusterName)

		go client.SendMsg(sendMsgNumber)

		client.Start()
	} else {
		nodeNumStr := os.Args[3]
		// 将字符串转换为整数
		nodeNum, _ := strconv.Atoi(nodeNumStr)
		consensus.F = nodeNum / 4
		server := network.NewServer(nodeID, clusterName)
		network.ClusterNumber, _ = strconv.Atoi(os.Args[4])

		server.Start()
	}

}
