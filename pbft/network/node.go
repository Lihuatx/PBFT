package network

import (
	"bufio"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"simple_pbft/pbft/consensus"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Node struct {
	NodeID         string
	NodeTable      map[string]map[string]string // key=nodeID, value=url
	NodeType       MaliciousNode
	View           *View
	CurrentState   *consensus.State
	CommittedMsgs  []*consensus.RequestMsg // kinda block.
	MsgBuffer      *MsgBuffer
	MsgEntrance    chan interface{}
	MsgDelivery    chan interface{}
	MsgRequsetchan chan interface{}

	AcceptRequestTime map[int64]time.Time // req SequenceID -> Start time

	Alarm chan bool
	// 全局消息日志和临时消息缓冲区
	GlobalLog    *consensus.GlobalLog
	GlobalBuffer *GlobalBuffer
	GlobalViewID int64
	// 请求消息的锁
	MsgBufferLock *MsgBufferLock

	MsgDeliveryLock sync.Mutex

	GlobalViewIDLock sync.Mutex

	//RSA私钥
	rsaPrivKey []byte
	//RSA公钥
	rsaPubKey []byte

	//所属集群
	ClusterName string

	//全局消息接受通道和处理通道
	MsgGlobal         chan interface{}
	MsgGlobalDelivery chan interface{}
}

type MsgBufferLock struct {
	ReqMsgsLock        sync.Mutex
	PrePrepareMsgsLock sync.Mutex
	PrepareMsgsLock    sync.Mutex
	CommitMsgsLock     sync.Mutex
}

type GlobalBuffer struct {
	ReqMsg       []*consensus.GlobalShareMsg //其他集群的请求消息缓存
	consensusMsg []*consensus.LocalMsg       //本地节点的全局共识消息缓存
}

type MsgBuffer struct {
	ReqMsgs        []*consensus.RequestMsg
	PrePrepareMsgs []*consensus.PrePrepareMsg
	PrepareMsgs    []*consensus.VoteMsg
	CommitMsgs     []*consensus.VoteMsg
	BatchReqMsgs   []*consensus.BatchRequestMsg
}

type View struct {
	ID      int64
	Primary string
}

var PrimaryNode = map[string]string{
	"N": "N0",
	"M": "M0",
	"P": "P0",
	"J": "J0",
	"K": "K0",
}

type MaliciousNode int

const (
	NonMaliciousNode MaliciousNode = iota
	isMaliciousNode
)

var Allcluster = []string{"N", "M", "P", "J", "K"}
var ClusterNumber = 5
var IsMaliciousNode = "No"

const ResolvingTimeDuration = time.Millisecond * 1000 // 1 second.

func NewNode(nodeID string, clusterName string) *Node {
	const viewID = 10000000000 // temporary.
	node := &Node{
		// Hard-coded for test.
		NodeID: nodeID,

		View: &View{
			ID:      viewID,
			Primary: PrimaryNode[clusterName],
		},

		// Consensus-related struct
		CurrentState:  nil,
		CommittedMsgs: make([]*consensus.RequestMsg, 0),
		MsgBuffer: &MsgBuffer{
			ReqMsgs:        make([]*consensus.RequestMsg, 0),
			PrePrepareMsgs: make([]*consensus.PrePrepareMsg, 0),
			PrepareMsgs:    make([]*consensus.VoteMsg, 0),
			CommitMsgs:     make([]*consensus.VoteMsg, 0),
			BatchReqMsgs:   make([]*consensus.BatchRequestMsg, 0),
		},
		GlobalLog: &consensus.GlobalLog{
			MsgLogs: make(map[string]map[int64]*consensus.BatchRequestMsg),
		},
		GlobalBuffer: &GlobalBuffer{
			ReqMsg:       make([]*consensus.GlobalShareMsg, 0),
			consensusMsg: make([]*consensus.LocalMsg, 0),
		},
		MsgBufferLock: &MsgBufferLock{},
		// Channels
		MsgEntrance:       make(chan interface{}, 200),
		MsgDelivery:       make(chan interface{}, 200),
		MsgGlobal:         make(chan interface{}, 200),
		MsgGlobalDelivery: make(chan interface{}, 200),
		MsgRequsetchan:    make(chan interface{}, 200),
		AcceptRequestTime: make(map[int64]time.Time),

		Alarm: make(chan bool),

		// 所属集群
		ClusterName:  clusterName,
		GlobalViewID: viewID,
	}

	node.NodeTable = LoadNodeTable("nodetable.txt")

	if IsMaliciousNode != "No" {
		node.NodeType = isMaliciousNode
		fmt.Println("Is malicious Node")
	} else {
		node.NodeType = NonMaliciousNode
		//fmt.Println("Not malicious Node")
	}

	// 初始化全局消息日志
	for i := 0; i < ClusterNumber; i++ {
		if node.GlobalLog.MsgLogs[Allcluster[i]] == nil {
			node.GlobalLog.MsgLogs[Allcluster[i]] = make(map[int64]*consensus.BatchRequestMsg)
		}
	}
	//for _, key := range Allcluster {
	//	if node.GlobalLog.MsgLogs[key] == nil {
	//		node.GlobalLog.MsgLogs[key] = make(map[int64]*consensus.BatchRequestMsg)
	//	}
	//}

	// 初始化每个节点的分数为70分
	//for cluster, nodes := range node.NodeTable {
	//	node.ReScore[cluster] = make(map[string]uint8) // 为每个集群初始化内部 map
	//	for _, _nodeID := range nodes {
	//		node.ReScore[cluster][_nodeID] = 70
	//	}
	//}

	node.rsaPubKey = node.getPubKey(clusterName, nodeID)
	node.rsaPrivKey = node.getPivKey(clusterName, nodeID)
	node.CurrentState = consensus.CreateState(node.View.ID, -2)

	lastViewId = 0
	lastGlobalId = 0
	// 专门用于收取客户端请求,防止堵塞其他线程
	go node.resolveClientRequest()

	// Start message dispatcher
	go node.dispatchMsg()

	// Start alarm trigger
	go node.alarmToDispatcher()

	// Start message resolver
	go node.resolveMsg()

	// Start solve Global message
	go node.resolveGlobalMsg()

	return node
}

// LoadNodeTable 从指定的文件路径加载 NodeTable
func LoadNodeTable(filePath string) map[string]map[string]string {
	file, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	// 初始化 NodeTable
	nodeTable := make(map[string]map[string]string)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) == 3 {
			cluster, nodeID, address := parts[0], parts[1], parts[2]

			if _, ok := nodeTable[cluster]; !ok {
				nodeTable[cluster] = make(map[string]string)
			}

			nodeTable[cluster][nodeID] = address
		}
	}

	if err := scanner.Err(); err != nil {
		return nil
	}

	return nodeTable
}

func (node *Node) Broadcast(cluster string, msg interface{}, path string) map[string]error {
	errorMap := make(map[string]error)

	for nodeID, url := range node.NodeTable[cluster] {
		if nodeID == node.NodeID {
			continue
		}

		jsonMsg, err := json.Marshal(msg)
		if err != nil {
			errorMap[nodeID] = err
			continue
		}
		//fmt.Printf("Send to %s Size of JSON message: %d bytes\n", url+path, len(jsonMsg))
		send(url+path, jsonMsg)
		time.Sleep(2 * time.Millisecond)

	}

	if len(errorMap) == 0 {
		return nil
	} else {
		return errorMap
	}
}

// ShareLocalConsensus 本地达成共识后，主节点调用当前函数发送信息给其他集群的f+1个节点
func (node *Node) ShareLocalConsensus(msg *consensus.GlobalShareMsg, path string) error {
	jsonMsg, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	for i := 0; i < ClusterNumber; i++ {
		cluster := Allcluster[i]
		if cluster == node.ClusterName {
			continue
		}
		for i := 0; i < consensus.F+1; i++ {
			nodeID := cluster + strconv.Itoa(i)
			url, exists := node.NodeTable[cluster][nodeID]
			if !exists {
				fmt.Printf("NodeID %s not found in nodeMsg\n", nodeID)
				continue
			}
			fmt.Printf("Send to %s Size of JSON message: %d bytes\n", url+path, len(jsonMsg))
			send(url+path, jsonMsg)
		}
	}
	return nil
}

var start time.Time
var duration time.Duration

func (node *Node) Reply(ViewID int64) (bool, int64) {
	for i := 0; i < ClusterNumber; i++ { //检查是否已经收到所有集群的消息
		_, ok := node.GlobalLog.MsgLogs[Allcluster[i]]
		if !ok {
			fmt.Printf("1\n")
			return false, 0
		}
	}
	//for _, value := range Allcluster { //检查是否已经收到所有集群的消息
	//	_, ok := node.GlobalLog.MsgLogs[value]
	//	if !ok {
	//		fmt.Printf("1\n")
	//		return false, 0
	//	}
	//}
	for i := 0; i < ClusterNumber; i++ { //检查是否已经收到所有集群当前阶段的可执行的消息
		_, ok := node.GlobalLog.MsgLogs[Allcluster[i]][ViewID]
		if !ok {
			fmt.Printf("2\n")
			return false, 0
		}
	}
	//for _, value := range Allcluster { //检查是否已经收到所有集群当前阶段的可执行的消息
	//	_, ok := node.GlobalLog.MsgLogs[value][ViewID]
	//	if !ok {
	//		fmt.Printf("2 %s\n", value)
	//		return false, 0
	//	}
	//}
	fmt.Printf("Global View ID : %d 达成全局共识\n", node.GlobalViewID)

	//for i := 0; i < ClusterNumber; i++ { //检查是否已经收到所有集群当前阶段的可执行的消息
	//	msg := node.GlobalLog.MsgLogs[Allcluster[i]][ViewID]
	//
	//	for i := 0; i < consensus.BatchSize; i++ {
	//		node.CommittedMsgs = append(node.CommittedMsgs, msg.Requests[i])
	//		fmt.Printf("CommittedMsg: %v ", msg.Requests[i].Operation)
	//	}
	//}
	//for _, value := range Allcluster {
	//	msg := node.GlobalLog.MsgLogs[value][ViewID]
	//	// Print all committed messages.
	//	//fmt.Printf("Committed value: %s, %d, %s, %d", msg.ClientID, msg.Timestamp, msg.Operation, msg.SequenceID)
	//
	//	for i := 0; i < consensus.BatchSize; i++ {
	//		node.CommittedMsgs = append(node.CommittedMsgs, msg.Requests[i])
	//	}
	//}
	fmt.Print("\n\n\n\n\n")

	node.GlobalViewID++
	const viewID = 10000000000 // temporary.
	if len(node.CommittedMsgs) == 1 {
		//start = time.Now()
	} else if len(node.CommittedMsgs) == 3000 && node.NodeID == "N0" {
		duration = time.Since(start)
		// 打开文件，如果文件不存在则创建，如果文件存在则追加内容
		fmt.Printf("  Function took %s\n", duration)
		//fmt.Printf("  Function took %s\n", duration)
		//fmt.Printf("  Function took %s\n", duration)

		file, err := os.OpenFile("example.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		// 使用fmt.Fprintf格式化写入内容到文件
		_, err = fmt.Fprintf(file, "durtion: %s\n", duration)
		if err != nil {
			log.Fatal(err)
		}

	} else if len(node.CommittedMsgs) > 3000 && node.NodeID == "N0" {
		fmt.Printf("  Function took %s\n", duration)
		//fmt.Printf("  Function took %s\n", duration)
		//fmt.Printf("  Function took %s\n", duration)
	}
	if node.NodeID == node.View.Primary { //主节点返回reply消息给客户端
		go func() {
			for i := 0; i < ClusterNumber; i++ { //检查是否已经收到所有集群当前阶段的可执行的消息
				msg := node.GlobalLog.MsgLogs[Allcluster[i]][ViewID]

				for i := 0; i < consensus.BatchSize; i++ {
					node.CommittedMsgs = append(node.CommittedMsgs, msg.Requests[i])
					//fmt.Printf("CommittedMsg: %v ", msg.Requests[i].Operation)
				}
			}
			ReplyMsg := node.GlobalLog.MsgLogs[node.ClusterName][ViewID]
			for i := 0; i < consensus.BatchSize; i++ {
				jsonMsg, _ := json.Marshal(ReplyMsg.Requests[i])
				// 系统中没有设置用户，reply消息直接发送给主节点
				url := ClientURL[node.ClusterName] + "/reply"
				send(url, jsonMsg)
				fmt.Printf("\nReply to Client!\n")
			}
		}()
	}
	return true, ViewID + 1
}

// GetReq can be called when the node's CurrentState is nil.
// Consensus start procedure for the Primary.
func (node *Node) GetReq(reqMsg *consensus.BatchRequestMsg, goOn bool) error {
	LogMsg(reqMsg)

	// Create a new state for the new consensus.
	err := node.createStateForNewConsensus(goOn)
	if err != nil {
		return err
	}

	// Start the consensus process.
	prePrepareMsg, err := node.CurrentState.StartConsensus(reqMsg)
	if err != nil {
		return err
	}

	// 主节点对消息摘要进行签名
	digestByte, _ := hex.DecodeString(prePrepareMsg.Digest)
	signInfo := node.RsaSignWithSha256(digestByte, node.rsaPrivKey)
	prePrepareMsg.Sign = signInfo

	LogStage(fmt.Sprintf("Consensus Process (ViewID:%d)", node.CurrentState.ViewID), false)

	// Send getPrePrepare message
	if prePrepareMsg != nil {
		// 附加主节点ID,用于数字签名验证
		prePrepareMsg.NodeID = node.NodeID

		node.Broadcast(node.ClusterName, prePrepareMsg, "/preprepare")
		LogStage("Pre-prepare", true)
	}

	return nil
}

// GetPrePrepare can be called when the node's CurrentState is nil.
// Consensus start procedure for normal participants.
func (node *Node) GetPrePrepare(prePrepareMsg *consensus.PrePrepareMsg, goOn bool) error {
	LogMsg(prePrepareMsg)
	node.AcceptRequestTime[prePrepareMsg.SequenceID] = time.Now()

	// Create a new state for the new consensus.
	err := node.createStateForNewConsensus(goOn)
	if err != nil {
		return err
	}
	// fmt.Printf("get Pre\n")
	digest, _ := hex.DecodeString(prePrepareMsg.Digest)
	if !node.RsaVerySignWithSha256(digest, prePrepareMsg.Sign, node.getPubKey(node.ClusterName, prePrepareMsg.NodeID)) {
		fmt.Println("节点签名验证失败！,拒绝执行Preprepare")
		return nil
	}
	prePareMsg, err := node.CurrentState.PrePrepare(prePrepareMsg)
	if err != nil {
		fmt.Println(err)
		return nil
	}

	if prePareMsg != nil {
		// Attach node ID to the message 同时对摘要签名
		prePareMsg.NodeID = node.NodeID
		signInfo := node.RsaSignWithSha256(digest, node.rsaPrivKey)
		prePareMsg.Sign = signInfo

		LogStage("Pre-prepare", true)
		if node.NodeType == isMaliciousNode {
			prePareMsg.SequenceID = 0
			//time.Sleep(100 * time.Millisecond)
		}

		node.Broadcast(node.ClusterName, prePareMsg, "/prepare")
		LogStage("Prepare", false)
	}

	return nil
}

func (node *Node) GetPrepare(prepareMsg *consensus.VoteMsg) error {
	LogMsg(prepareMsg)

	digest, _ := hex.DecodeString(prepareMsg.Digest)
	if !node.RsaVerySignWithSha256(digest, prepareMsg.Sign, node.getPubKey(node.ClusterName, prepareMsg.NodeID)) {
		fmt.Println("节点签名验证失败！,拒绝执行prepare")
	}
	//主节点是不广播prepare的，所以为自己投一票
	if node.CurrentState.MsgLogs.PrepareMsgs[node.NodeID] == nil && node.NodeID != node.View.Primary {
		node.CurrentState.MsgLogs.PrepareMsgs[node.NodeID] = prepareMsg
	}
	commitMsg, err := node.CurrentState.Prepare(prepareMsg)
	if err != nil {
		ErrMessage(prepareMsg)
		return err
	}
	if commitMsg != nil {
		// Attach node ID to the message 同时对摘要签名
		commitMsg.NodeID = node.NodeID
		signInfo := node.RsaSignWithSha256(digest, node.rsaPrivKey)
		commitMsg.Sign = signInfo

		LogStage("Prepare", true)
		if node.NodeType == isMaliciousNode {
			commitMsg.SequenceID = 0
			//time.Sleep(100 * time.Millisecond)
		}

		node.Broadcast(node.ClusterName, commitMsg, "/commit")
		LogStage("Commit", false)
	}

	return nil
}

func (node *Node) GetCommit(commitMsg *consensus.VoteMsg) error {
	// 当节点已经完成Committed阶段后就停止接收其他节点的Committed消息
	if node.CurrentState.CurrentStage == consensus.Committed {
		return nil
	}

	LogMsg(commitMsg)

	digest, _ := hex.DecodeString(commitMsg.Digest)
	if !node.RsaVerySignWithSha256(digest, commitMsg.Sign, node.getPubKey(node.ClusterName, commitMsg.NodeID)) {
		fmt.Println("节点签名验证失败！,拒绝执行commit")
	}

	replyMsg, committedMsg, err := node.CurrentState.Commit(commitMsg)
	if err != nil {
		ErrMessage(committedMsg)
		return err
	}
	// 达成本地Committed共识
	if replyMsg != nil {

		if committedMsg == nil {
			return errors.New("committed message is nil, even though the reply message is not nil")
		}

		// Attach node ID to the message
		replyMsg.NodeID = node.NodeID

		// Save the last version of committed messages to node.
		// node.CommittedMsgs = append(node.CommittedMsgs, committedMsg)

		LogStage("Commit", true)
		fmt.Printf("ViewID :%d 达成本地共识，存入待执行缓存池\n", node.View.ID)

		// Append msg to its logs
		node.GlobalLog.MsgLogs[node.ClusterName][node.View.ID] = committedMsg

		if node.NodeID == node.View.Primary { // 本地共识结束后，主节点将本地达成共识的请求发送至其他集群的主节点
			fmt.Printf("send consensus to Global\n")
			// 获取消息摘要
			msg, err := json.Marshal(committedMsg)
			if err != nil {
				return err
			}
			digest := consensus.Hash(msg)

			// 节点对消息摘要进行签名
			digestByte, _ := hex.DecodeString(digest)
			signInfo := node.RsaSignWithSha256(digestByte, node.rsaPrivKey)
			// committedMsg.Result = false
			GlobalShareMsg := new(consensus.GlobalShareMsg)
			GlobalShareMsg.RequestMsg = committedMsg
			GlobalShareMsg.NodeID = node.NodeID
			GlobalShareMsg.Sign = signInfo
			GlobalShareMsg.Digest = digest
			GlobalShareMsg.Cluster = node.ClusterName
			GlobalShareMsg.ViewID = node.View.ID

			Sstart := time.Now()
			node.ShareLocalConsensus(GlobalShareMsg, "/global")
			end := time.Since(Sstart)

			file, err := os.OpenFile("PrimaryShareToGlobal.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				log.Fatal(err)
			}
			defer file.Close()
			// 使用fmt.Fprintf格式化写入内容到文件
			_, err = fmt.Fprintf(file, "NodeNum:%d  PrimaryShareToGlobal Used Time: %s\n", consensus.F*3, end)
			if err != nil {
				log.Fatal(err)
			}
		} else {
			re := regexp.MustCompile(`[0-9]+`)
			matches := re.FindStringSubmatch(node.NodeID)
			numberStr := matches[0]                 // 提取到的数字部分作为字符串
			numberInt, _ := strconv.Atoi(numberStr) // 将字符串转换为整数
			if numberInt == 1 {
				CompleteTime := time.Since(node.AcceptRequestTime[commitMsg.SequenceID])
				file, err := os.OpenFile("LocalConsensusCompleteTime.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
				if err != nil {
					log.Fatal(err)
				}
				defer file.Close()
				// 使用fmt.Fprintf格式化写入内容到文件
				_, err = fmt.Fprintf(file, "Node Number %d  LocalConsensusCompleteTime: %s\n", consensus.F*3, CompleteTime)
				if err != nil {
					log.Fatal(err)
				}
			}
		}
		node.View.ID++
		node.CurrentState.CurrentStage = consensus.Committed

		// 达成本地共识，检查能否进行全局共识的排序和执行
		node.GlobalViewIDLock.Lock()
		if node.GlobalViewID == commitMsg.ViewID {
			node.Reply(node.GlobalViewID)
		}
		node.GlobalViewIDLock.Unlock()

	}
	return nil
}

func (node *Node) GetReply(msg *consensus.ReplyMsg) {
	fmt.Printf("Result: %s by %s\n", msg.Result, msg.NodeID)
}

func (node *Node) createStateForNewConsensus(goOn bool) error {
	const viewID = 10000000000 // temporary.
	// Check if there is an ongoing consensus process.
	if node.CurrentState.LastSequenceID != -2 {
		if node.CurrentState.CurrentStage != consensus.Committed && !goOn && node.CurrentState.CurrentStage != consensus.GetRequest {
			return errors.New("another consensus is ongoing")
		}
	}
	// Get the last sequence ID
	var lastSequenceID int64
	if node.View.ID == viewID {
		lastSequenceID = -1
	} else {
		lastSequenceID = node.GlobalLog.MsgLogs[node.ClusterName][node.View.ID-1].Requests[0].SequenceID
	}

	// Create a new state for this new consensus process in the Primary
	node.CurrentState = consensus.CreateState(node.View.ID, lastSequenceID)

	LogStage("Create the replica status", true)
	return nil
}

func (node *Node) dispatchMsg() {
	for {
		time.Sleep(10 * time.Microsecond)

		select {
		case msg := <-node.MsgEntrance:
			err := node.routeMsg(msg)
			if err != nil {
				fmt.Println(err)
				// TODO: send err to ErrorChannel
			}
		case <-node.Alarm:
			err := node.routeMsgWhenAlarmed()
			if err != nil {
				fmt.Println(err)
				// TODO: send err to ErrorChannel
			}
		case msg := <-node.MsgGlobal:
			err := node.routeGlobalMsg(msg)
			if err != nil {
				fmt.Println(err)
				// TODO: send err to ErrorChannel
			}
		}
	}

}

func (node *Node) routeGlobalMsg(msg interface{}) []error {
	//switch m := msg.(type) {
	switch msg.(type) {
	case *consensus.GlobalShareMsg:
		//fmt.Printf("---- Receive the Global Consensus from %s for Global ID:%d\n", m.NodeID, m.ViewID)
		// Copy buffered messages first.
		msgs := make([]*consensus.GlobalShareMsg, len(node.GlobalBuffer.ReqMsg))
		copy(msgs, node.GlobalBuffer.ReqMsg)

		// Append a newly arrived message.
		msgs = append(msgs, msg.(*consensus.GlobalShareMsg))

		// Empty the buffer.
		node.GlobalBuffer.ReqMsg = make([]*consensus.GlobalShareMsg, 0)

		// Send messages.
		node.MsgGlobalDelivery <- msgs
	case *consensus.LocalMsg:
		//fmt.Printf("---- Receive the Local Consensus from %s for cluster %s Global ID:%d\n", m.NodeID, m.GlobalShareMsg.Cluster, m.GlobalShareMsg.ViewID)
		// Copy buffered messages first.
		msgs := make([]*consensus.LocalMsg, len(node.GlobalBuffer.consensusMsg))
		copy(msgs, node.GlobalBuffer.consensusMsg)

		// Append a newly arrived message.
		msgs = append(msgs, msg.(*consensus.LocalMsg))

		// Empty the buffer.
		node.GlobalBuffer.consensusMsg = make([]*consensus.LocalMsg, 0)

		// Send messages.
		node.MsgGlobalDelivery <- msgs
	}

	return nil
}

func (node *Node) SaveClientRequest(msg interface{}) {
	switch msg.(type) {
	case *consensus.RequestMsg:
		//for {
		//	if len(node.MsgBuffer.ReqMsgs) <= 20 {
		//		break
		//	}
		//}
		//一开始没有进行共识的时候，此时 currentstate 为nil
		node.MsgBufferLock.ReqMsgsLock.Lock()
		node.MsgBuffer.ReqMsgs = append(node.MsgBuffer.ReqMsgs, msg.(*consensus.RequestMsg))
		node.MsgBufferLock.ReqMsgsLock.Unlock()
		fmt.Printf("缓存中收到 %d 条客户端请求\n", len(node.MsgBuffer.ReqMsgs))
	}
}

func (node *Node) resolveClientRequest() {
	for {
		select {
		case msg := <-node.MsgRequsetchan:
			node.SaveClientRequest(msg)
			time.Sleep(10 * time.Microsecond)
		}
	}
}

func (node *Node) routeMsg(msg interface{}) []error {
	switch msg.(type) {
	case *consensus.PrePrepareMsg:

		node.MsgBufferLock.PrePrepareMsgsLock.Lock()
		node.MsgBuffer.PrePrepareMsgs = append(node.MsgBuffer.PrePrepareMsgs, msg.(*consensus.PrePrepareMsg))
		node.MsgBufferLock.PrePrepareMsgsLock.Unlock()
		//fmt.Printf("                    Msgbuffer %d %d %d %d\n", len(node.MsgBuffer.ReqMsgs), len(node.MsgBuffer.PrePrepareMsgs), len(node.MsgBuffer.PrepareMsgs), len(node.MsgBuffer.CommitMsgs))

	case *consensus.VoteMsg:
		if msg.(*consensus.VoteMsg).MsgType == consensus.PrepareMsg {
			// if node.CurrentState == nil || node.CurrentState.CurrentStage != consensus.PrePrepared
			// 这样的写法会导致当当前节点已经收到2f个节点进入committed阶段时，就会把后来收到的Preprepare消息放到缓冲区中，
			// 这样在下次共识又到prePrepare阶段时就会先去处理上一轮共识的prePrepare协议！
			node.MsgBufferLock.PrepareMsgsLock.Lock()
			node.MsgBuffer.PrepareMsgs = append(node.MsgBuffer.PrepareMsgs, msg.(*consensus.VoteMsg))
			node.MsgBufferLock.PrepareMsgsLock.Unlock()
		} else if msg.(*consensus.VoteMsg).MsgType == consensus.CommitMsg {
			node.MsgBufferLock.CommitMsgsLock.Lock()
			node.MsgBuffer.CommitMsgs = append(node.MsgBuffer.CommitMsgs, msg.(*consensus.VoteMsg))
			node.MsgBufferLock.CommitMsgsLock.Unlock()
		}

		//fmt.Printf("                    Msgbuffer %d %d %d %d\n", len(node.MsgBuffer.ReqMsgs), len(node.MsgBuffer.PrePrepareMsgs), len(node.MsgBuffer.PrepareMsgs), len(node.MsgBuffer.CommitMsgs))
	}

	return nil
}

var lastViewId int64
var lastGlobalId int64

func (node *Node) routeMsgWhenAlarmed() []error {
	if node.View.ID != lastViewId || node.GlobalViewID != lastGlobalId {
		fmt.Printf("                                                                View ID %d,Global ID %d\n", node.View.ID, node.GlobalViewID)
		lastViewId = node.View.ID
		lastGlobalId = node.GlobalViewID
	}
	//if node.CurrentState.LastSequenceID == -2 || node.CurrentState.CurrentStage == consensus.Committed {
	//	// Check ReqMsgs, send them.
	//	if len(node.MsgBuffer.ReqMsgs) != 0 {
	//		msgs := make([]*consensus.RequestMsg, len(node.MsgBuffer.ReqMsgs))
	//		copy(msgs, node.MsgBuffer.ReqMsgs)
	//
	//		node.MsgDelivery <- msgs
	//	}
	//
	//	// Check PrePrepareMsgs, send them.
	//	if len(node.MsgBuffer.PrePrepareMsgs) != 0 {
	//		msgs := make([]*consensus.PrePrepareMsg, len(node.MsgBuffer.PrePrepareMsgs))
	//		copy(msgs, node.MsgBuffer.PrePrepareMsgs)
	//
	//		node.MsgDelivery <- msgs
	//	}
	//
	//	// 查看是否要发送Empty消息
	//	g := []string{"N", "M"}
	//	flag := false
	//	// 如果当前的已经有收到其他集群达成的共识，且本集群没有收到request，发送empty消息！
	//	for _, value := range g {
	//		if value == node.ClusterName {
	//			continue
	//		} else {
	//			if node.GlobalLog.MsgLogs[value][node.GlobalViewID] != nil {
	//				flag = true
	//			}
	//		}
	//	}
	//	if flag && len(node.MsgBuffer.ReqMsgs) == 0 {
	//		/*
	//			fmt.Printf("Start Empty consensus for ViewID %d in ShareGlobalMsgToLocal\n", node.GlobalViewID)
	//			var msg consensus.RequestMsg
	//			msg.ClientID = node.NodeID
	//			msg.Operation = "Empty"
	//			msg.Timestamp = 0
	//			node.MsgEntrance <- &msg
	//		*/
	//	}
	//} else {
	//	switch node.CurrentState.CurrentStage {
	//	case consensus.PrePrepared:
	//		// Check PrepareMsgs, send them.
	//		if len(node.MsgBuffer.PrepareMsgs) != 0 {
	//			msgs := make([]*consensus.VoteMsg, len(node.MsgBuffer.PrepareMsgs))
	//			copy(msgs, node.MsgBuffer.PrepareMsgs)
	//
	//			node.MsgDelivery <- msgs
	//		}
	//	case consensus.Prepared:
	//		// Check CommitMsgs, send them.
	//		if len(node.MsgBuffer.CommitMsgs) != 0 {
	//			msgs := make([]*consensus.VoteMsg, len(node.MsgBuffer.CommitMsgs))
	//			copy(msgs, node.MsgBuffer.CommitMsgs)
	//
	//			node.MsgDelivery <- msgs
	//		}
	//	}
	//}

	return nil
}

func (node *Node) resolveGlobalMsg() {
	for {
		time.Sleep(10 * time.Microsecond)

		msg := <-node.MsgGlobalDelivery
		switch msg.(type) {
		case []*consensus.GlobalShareMsg:
			errs := node.resolveGlobalShareMsg(msg.([]*consensus.GlobalShareMsg))
			if len(errs) != 0 {
				for _, err := range errs {
					fmt.Println(err)
				}
				// TODO: send err to ErrorChannel
			}
		case []*consensus.LocalMsg:
			errs := node.resolveLocalMsg(msg.([]*consensus.LocalMsg))
			if len(errs) != 0 {
				for _, err := range errs {
					fmt.Println(err)
				}
				// TODO: send err to ErrorChannel
			}
		}
	}
}

// 出队
// Dequeue for Request messages
func (mb *MsgBuffer) DequeueReqMsg() *consensus.RequestMsg {
	if len(mb.ReqMsgs) == 0 {
		return nil
	}
	msg := mb.ReqMsgs[0]                          // 获取第一个元素
	mb.ReqMsgs = mb.ReqMsgs[consensus.BatchSize:] // 移除第一个元素
	return msg
}

// Dequeue for PrePrepare messages
func (mb *MsgBuffer) DequeuePrePrepareMsg() *consensus.PrePrepareMsg {
	if len(mb.PrePrepareMsgs) == 0 {
		return nil
	}
	msg := mb.PrePrepareMsgs[0]
	mb.PrePrepareMsgs = mb.PrePrepareMsgs[1:]
	return msg
}

func (node *Node) resolveMsg() {
	for {
		time.Sleep(10 * time.Microsecond)

		// Get buffered messages from the dispatcher.
		switch {
		case len(node.MsgBuffer.ReqMsgs) >= consensus.BatchSize && (node.CurrentState.LastSequenceID == -2 || node.CurrentState.CurrentStage == consensus.Committed):
			node.MsgBufferLock.ReqMsgsLock.Lock()
			// 初始化batch并确保它是非nil
			var batch consensus.BatchRequestMsg
			const viewID = 10000000000
			// 逐个赋值到数组中
			for j := 0; j < consensus.BatchSize; j++ {
				batch.Requests[j] = node.MsgBuffer.ReqMsgs[j]
			}
			batch.Timestamp = node.MsgBuffer.ReqMsgs[0].Timestamp
			batch.ClientID = node.MsgBuffer.ReqMsgs[0].ClientID
			// batch.Send = false
			// 添加新的批次到批次消息缓存
			node.MsgBuffer.BatchReqMsgs = append(node.MsgBuffer.BatchReqMsgs, &batch)

			errs := node.resolveRequestMsg(node.MsgBuffer.BatchReqMsgs[node.View.ID-viewID])
			if errs != nil {
				fmt.Println(errs)
				// TODO: send err to ErrorChannel
			}
			node.MsgBuffer.DequeueReqMsg()
			node.MsgBufferLock.ReqMsgsLock.Unlock()
		case len(node.MsgBuffer.PrePrepareMsgs) > 0 && (node.CurrentState.LastSequenceID == -2 || node.CurrentState.CurrentStage == consensus.Committed):
			node.MsgBufferLock.PrePrepareMsgsLock.Lock()
			errs := node.resolvePrePrepareMsg(node.MsgBuffer.PrePrepareMsgs[0])
			if errs != nil {
				fmt.Println(errs)
				// TODO: send err to ErrorChannel
			}
			node.MsgBuffer.DequeuePrePrepareMsg()
			node.MsgBufferLock.PrePrepareMsgsLock.Unlock()
		case len(node.MsgBuffer.PrepareMsgs) > 0 && node.CurrentState.CurrentStage == consensus.PrePrepared:
			node.MsgBufferLock.PrepareMsgsLock.Lock()
			var keepIndexes []int     // 用于存储需要保留的元素的索引
			var processIndex int = -1 // 用于存储第一个符合条件的元素的索引，初始化为-1表示未找到
			// 首先遍历PrepareMsgs，确定哪些元素需要保留，哪个元素需要处理
			for index, value := range node.MsgBuffer.PrepareMsgs {
				if value.ViewID < node.View.ID {
					// 不需要做任何事，因为这个元素将被删除
				} else if value.ViewID > node.View.ID {
					keepIndexes = append(keepIndexes, index) // 保留这个元素
				} else if processIndex == -1 { // 只记录第一个符合条件的元素
					processIndex = index
				} else {
					keepIndexes = append(keepIndexes, index)
				}
			}
			// 如果找到了符合条件的元素，则处理它
			if processIndex != -1 {
				errs := node.resolvePrepareMsg(node.MsgBuffer.PrepareMsgs[processIndex])
				// 将这个元素标记为已处理，不再保留
				if errs != nil {
					fmt.Println(errs)
					// TODO: send err to ErrorChannel
				}
			}
			// 创建一个新的切片来存储保留的元素
			var newPrepareMsgs []*consensus.VoteMsg // 假设YourMsgType是PrepareMsgs中元素的类型
			for _, index := range keepIndexes {
				newPrepareMsgs = append(newPrepareMsgs, node.MsgBuffer.PrepareMsgs[index])
			}

			// 更新原来的PrepareMsgs为只包含保留元素的新切片
			node.MsgBuffer.PrepareMsgs = newPrepareMsgs

			node.MsgBufferLock.PrepareMsgsLock.Unlock()

			//errs := node.resolvePrepareMsg(node.MsgBuffer.PrepareMsgs[0])
			//if errs != nil {
			//
			//	fmt.Println(errs)
			//
			//	// TODO: send err to ErrorChannel
			//}
			//node.MsgBufferLock.PrepareMsgsLock.Lock()
			//node.MsgBuffer.DequeuePrepareMsg()
			//node.MsgBufferLock.PrepareMsgsLock.Unlock()
		case len(node.MsgBuffer.CommitMsgs) > 0 && (node.CurrentState.CurrentStage == consensus.Prepared):
			node.MsgBufferLock.CommitMsgsLock.Lock()
			var keepIndexes []int // 用于存储需要保留的元素的索引
			var processIndex = -1 // 用于存储第一个符合条件的元素的索引，初始化为-1表示未找到
			// 首先遍历PrepareMsgs，确定哪些元素需要保留，哪个元素需要处理
			for index, value := range node.MsgBuffer.CommitMsgs {
				if value.ViewID < node.View.ID {
					// 不需要做任何事，因为这个元素将被删除
				} else if value.ViewID > node.View.ID {
					keepIndexes = append(keepIndexes, index) // 保留这个元素
				} else if processIndex == -1 { // 只记录第一个符合条件的元素
					processIndex = index
				} else {
					keepIndexes = append(keepIndexes, index)
				}
			}
			// 如果找到了符合条件的元素，则处理它
			if processIndex != -1 {
				errs := node.resolveCommitMsg(node.MsgBuffer.CommitMsgs[processIndex])
				// 将这个元素标记为已处理，不再保留
				if errs != nil {
					fmt.Println(errs)
					// TODO: send err to ErrorChannel
				}
			}
			// 创建一个新的切片来存储保留的元素
			var newCommitMsgs []*consensus.VoteMsg // 假设YourMsgType是PrepareMsgs中元素的类型
			for _, index := range keepIndexes {
				newCommitMsgs = append(newCommitMsgs, node.MsgBuffer.CommitMsgs[index])
			}

			// 更新原来的PrepareMsgs为只包含保留元素的新切片
			node.MsgBuffer.CommitMsgs = newCommitMsgs

			node.MsgBufferLock.CommitMsgsLock.Unlock()

		default:

		}

	}
}

func (node *Node) alarmToDispatcher() {
	for {
		time.Sleep(ResolvingTimeDuration)
		node.Alarm <- true
	}
}

func (node *Node) resolveRequestMsg(msg *consensus.BatchRequestMsg) error {

	err := node.GetReq(msg, false)
	if err != nil {
		return err
	}

	return nil
}

func (node *Node) resolveGlobalShareMsg(msgs []*consensus.GlobalShareMsg) []error {
	errs := make([]error, 0)

	// Resolve messages
	fmt.Printf("len GlobalShareMsg msg %d\n", len(msgs))

	for _, reqMsg := range msgs {
		// 收到其他组的消息，转发给本地节点
		err := node.ShareGlobalMsgToLocal(reqMsg)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) != 0 {
		return errs
	}

	return nil
}

func (node *Node) resolveLocalMsg(msgs []*consensus.LocalMsg) []error {
	errs := make([]error, 0)

	// Resolve messages
	fmt.Printf("len LocalGlobalShareMsg msg %d\n", len(msgs))

	for _, reqMsg := range msgs {

		// 收到本地节点发来的全局共识消息，投票
		err := node.CommitGlobalMsgToLocal(reqMsg)
		if err != nil {
			errs = append(errs, err)
		}

	}

	if len(errs) != 0 {
		return errs
	}

	return nil
}

func (node *Node) GlobalConsensus(msg *consensus.LocalMsg) (*consensus.ReplyMsg, *consensus.BatchRequestMsg, error) {
	// Print current voting status
	fmt.Printf("-----Global-Commit-Save For %s----\n", msg.GlobalShareMsg.Cluster)

	// This node executes the requested operation locally and gets the result.
	result := "Executed"

	// Change the stage to prepared.
	return &consensus.ReplyMsg{
		ViewID:    msg.GlobalShareMsg.ViewID,
		Timestamp: 0,
		ClientID:  msg.GlobalShareMsg.RequestMsg.ClientID,
		Result:    result,
	}, msg.GlobalShareMsg.RequestMsg, nil

	// return nil, nil, nil
}

// CommitGlobalMsgToLocal 收到本地节点发来的全局共识消息
func (node *Node) CommitGlobalMsgToLocal(reqMsg *consensus.LocalMsg) error {
	// LogMsg(reqMsg)

	digest, _ := hex.DecodeString(reqMsg.GlobalShareMsg.Digest)
	if !node.RsaVerySignWithSha256(digest, reqMsg.Sign, node.getPubKey(node.ClusterName, reqMsg.NodeID)) {
		fmt.Println("节点签名验证失败！,拒绝执行Global commit")
	}

	// Append msg to its logs
	node.GlobalLog.MsgLogs[reqMsg.GlobalShareMsg.Cluster][reqMsg.GlobalShareMsg.ViewID] = reqMsg.GlobalShareMsg.RequestMsg

	// 如果是主节点收到其他集群的全局共享消息，需要检查本地有正在进行的共识或收到客户端的消息或者本地共识是否已完成，如果都没有需要发送一个空白消息进行本地共识
	if node.NodeID == node.View.Primary {
		// 检查本地有没有正在进行的共识？
		if node.CurrentState.LastSequenceID == -2 || node.CurrentState.CurrentStage == consensus.Committed {
			// 检查有没有收到客户端的消息
			node.MsgBufferLock.ReqMsgsLock.Lock()
			node.MsgDeliveryLock.Lock()
			if len(node.MsgBuffer.ReqMsgs) == 0 && len(node.MsgEntrance) == 0 && len(node.MsgDelivery) == 0 {

			}
			node.MsgDeliveryLock.Unlock()
			node.MsgBufferLock.ReqMsgsLock.Unlock()
		}
	}

	// GlobalConsensus 会将msg存入MsgLogs中
	replyMsg, committedMsg, err := node.GlobalConsensus(reqMsg)
	if err != nil {
		ErrMessage(committedMsg)
		return err
	}

	if replyMsg != nil {
		if committedMsg == nil {
			return errors.New("committed message is nil, even though the reply message is not nil")
		}

		// Attach node ID to the message
		replyMsg.NodeID = node.NodeID
		// Save the last version of committed messages to node.
		// node.CommittedMsgs = append(node.CommittedMsgs, committedMsg)
		fmt.Printf("Global stage ID %s %d\n", reqMsg.GlobalShareMsg.Cluster, reqMsg.GlobalShareMsg.ViewID)
		//fmt.Printf("-----Overall consensus----\n")
		node.GlobalViewIDLock.Lock()
		if node.GlobalViewID == reqMsg.GlobalShareMsg.ViewID {
			node.Reply(node.GlobalViewID)
		}
		node.GlobalViewIDLock.Unlock()
		// LogStage("Reply\n", true)
	}

	return nil
}

// 收到其他集群主节点发来的共识消息
func (node *Node) ShareGlobalMsgToLocal(reqMsg *consensus.GlobalShareMsg) error {
	// LogMsg(reqMsg)
	// LogStage(fmt.Sprintf("Consensus Process (ViewID:%d)", node.CurrentState.ViewID), false)
	digest, _ := hex.DecodeString(reqMsg.Digest)
	if !node.RsaVerySignWithSha256(digest, reqMsg.Sign, node.getPubKey(reqMsg.Cluster, reqMsg.NodeID)) {
		fmt.Println("节点签名验证失败！,拒绝执行Global commit")
	}

	if reqMsg.NodeID != PrimaryNode[reqMsg.Cluster] {
		fmt.Printf("非 %s 主节点发送的全局共识，拒绝接受", reqMsg.Cluster)
		return nil
	}

	// 节点对消息摘要进行签名
	signInfo := node.RsaSignWithSha256(digest, node.rsaPrivKey)
	// LogStage(fmt.Sprintf("Consensus Process (ViewID:%d)", node.CurrentState.ViewID), false)

	// Send getPrePrepare message

	// 附加节点ID,用于数字签名验证
	sendMsg := &consensus.LocalMsg{
		Sign:           signInfo,
		NodeID:         node.NodeID,
		GlobalShareMsg: reqMsg,
	}

	// 将消息存入log中
	node.GlobalLog.MsgLogs[reqMsg.Cluster][reqMsg.ViewID] = reqMsg.RequestMsg

	node.Broadcast(node.ClusterName, sendMsg, "/GlobalToLocal")
	fmt.Printf("----- GlobalToLocal -----\n")

	//如果是主节点收到其他集群的全局共享消息，需要检查本地有正在进行的共识或收到客户端的消息，如果都没有需要发送一个空白消息进行本地共识
	if node.NodeID == node.View.Primary {
		// 检查本地有没有正在进行的共识？
		if node.CurrentState == nil || node.CurrentState.CurrentStage == consensus.Committed {
			// 检查有没有收到客户端的消息
			node.MsgBufferLock.ReqMsgsLock.Lock()
			node.MsgDeliveryLock.Lock()
			if len(node.MsgBuffer.ReqMsgs) == 0 && len(node.MsgEntrance) == 0 && len(node.MsgDelivery) == 0 {
				/*
					_, ok := node.GlobalLog.MsgLogs[node.ClusterName][reqMsg.ViewID]
					if !ok && reqMsg.RequestMsg.Operation != "Empty" && node.GlobalLog.MsgLogs[reqMsg.Cluster][node.GlobalViewID].Operation != "Empty" {
						fmt.Printf("Start Empty consensus for ViewID %d in ShareGlobalMsgToLocal\n", reqMsg.ViewID)
						var msg consensus.RequestMsg
						msg.ClientID = node.NodeID
						msg.Operation = "Empty"
						msg.Timestamp = 0
						node.MsgEntrance <- &msg
					}*/
			}
			node.MsgDeliveryLock.Unlock()
			node.MsgBufferLock.ReqMsgsLock.Unlock()
		}
	}

	return nil
}

func (node *Node) resolvePrePrepareMsg(msg *consensus.PrePrepareMsg) error {

	// Resolve messages
	// 从下标num_of_event_to_resolve开始执行，之前执行过的PrePrepareMsg不需要再执行
	///fmt.Printf("len PrePrepareMsg msg %d\n", len(msgs))
	err := node.GetPrePrepare(msg, false)

	if err != nil {
		return err
	}

	return nil
}

func (node *Node) resolvePrepareMsg(msg *consensus.VoteMsg) error {
	// Resolve messages
	///fmt.Printf("len PrepareMsg msg %d\n", len(msgs))
	if msg.ViewID < node.View.ID {
		return nil
	}
	err := node.GetPrepare(msg)

	if err != nil {
		return err
	}

	return nil
}

func (node *Node) resolveCommitMsg(msg *consensus.VoteMsg) error {
	if msg.ViewID < node.View.ID {
		return nil
	}

	err := node.GetCommit(msg)
	if err != nil {
		return err
	}

	return nil
}

// 传入节点编号， 获取对应的公钥
func (node *Node) getPubKey(ClusterName string, nodeID string) []byte {
	key, err := ioutil.ReadFile("Keys/" + ClusterName + "/" + nodeID + "/" + nodeID + "_RSA_PUB")
	if err != nil {
		log.Panic(err)
	}
	return key
}

// 传入节点编号， 获取对应的私钥
func (node *Node) getPivKey(ClusterName string, nodeID string) []byte {
	key, err := ioutil.ReadFile("Keys/" + ClusterName + "/" + nodeID + "/" + nodeID + "_RSA_PIV")
	if err != nil {
		log.Panic(err)
	}
	return key
}

// 数字签名
func (node *Node) RsaSignWithSha256(data []byte, keyBytes []byte) []byte {
	h := sha256.New()
	h.Write(data)
	hashed := h.Sum(nil)
	block, _ := pem.Decode(keyBytes)
	if block == nil {
		panic(errors.New("private key error"))
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		fmt.Println("ParsePKCS8PrivateKey err", err)
		panic(err)
	}

	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hashed)
	if err != nil {
		fmt.Printf("Error from signing: %s\n", err)
		panic(err)
	}

	return signature
}

// 签名验证
func (node *Node) RsaVerySignWithSha256(data, signData, keyBytes []byte) bool {
	block, _ := pem.Decode(keyBytes)
	if block == nil {
		panic(errors.New("public key error"))
	}
	pubKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic(err)
	}

	hashed := sha256.Sum256(data)
	err = rsa.VerifyPKCS1v15(pubKey.(*rsa.PublicKey), crypto.SHA256, hashed[:], signData)
	if err != nil {
		panic(err)
	}
	return true
}
