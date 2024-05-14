package network

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"simple_pbft/pbft/consensus"
	"time"
)

func init() {
	// 配置 http.DefaultTransport
	http.DefaultTransport.(*http.Transport).DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	http.DefaultTransport.(*http.Transport).MaxIdleConns = 200
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 100
	http.DefaultTransport.(*http.Transport).IdleConnTimeout = 90 * time.Second
	http.DefaultTransport.(*http.Transport).TLSHandshakeTimeout = 10 * time.Second
}

type Server struct {
	url  string
	node *Node
}

var flag = false

func NewServer(nodeID string, clusterName string) *Server {
	node := NewNode(nodeID, clusterName)
	server := &Server{node.NodeTable[clusterName][nodeID], node}

	server.setRoute()

	return server
}

func (server *Server) Start() {
	fmt.Printf("Server %v will be started at %s...\n", server.node.NodeID, server.url)
	if err := http.ListenAndServe(server.url, nil); err != nil {
		fmt.Println(err)
		return
	}
}

func (server *Server) setRoute() {
	http.HandleFunc("/req", server.getReq)
	http.HandleFunc("/preprepare", server.getPrePrepare)
	http.HandleFunc("/prepare", server.getPrepare)
	http.HandleFunc("/commit", server.getCommit)
	http.HandleFunc("/reply", server.getReply)
	//接受全局共识消息
	http.HandleFunc("/global", server.getGlobal)
	http.HandleFunc("/GlobalToLocal", server.getGlobalToLocal)

}

func (server *Server) getReq(writer http.ResponseWriter, request *http.Request) {
	var msg consensus.RequestMsg
	err := json.NewDecoder(request.Body).Decode(&msg)
	// for test
	if err != nil {
		fmt.Println(err)
		return
	}
	// 保存请求的URL到RequestMsg中
	msg.URL = request.URL.String()
	if !flag {
		start = time.Now()
		flag = true
	}
	fmt.Printf("\nhttp get RequestMsg op : %s\n", msg.Operation)
	server.node.MsgRequsetchan <- &msg
}

func (server *Server) getPrePrepare(writer http.ResponseWriter, request *http.Request) {
	var msg consensus.PrePrepareMsg
	err := json.NewDecoder(request.Body).Decode(&msg)

	if err != nil {
		fmt.Println(err)
		return
	}

	server.node.MsgEntrance <- &msg
}

func (server *Server) getPrepare(writer http.ResponseWriter, request *http.Request) {
	var msg consensus.VoteMsg
	err := json.NewDecoder(request.Body).Decode(&msg)
	if err != nil {
		fmt.Println(err)
		return
	}

	server.node.MsgEntrance <- &msg
}

func (server *Server) getCommit(writer http.ResponseWriter, request *http.Request) {
	var msg consensus.VoteMsg
	err := json.NewDecoder(request.Body).Decode(&msg)
	if err != nil {
		fmt.Println(err)
		return
	}

	server.node.MsgEntrance <- &msg
}

func (server *Server) getReply(writer http.ResponseWriter, request *http.Request) {
	var msg consensus.ReplyMsg
	err := json.NewDecoder(request.Body).Decode(&msg)
	if err != nil {
		fmt.Println(err)
		return
	}

	server.node.GetReply(&msg)
}

func (server *Server) getGlobal(writer http.ResponseWriter, request *http.Request) {
	var msg consensus.GlobalShareMsg
	err := json.NewDecoder(request.Body).Decode(&msg)
	if err != nil {
		fmt.Println(err)
		return
	}
	// fmt.Printf("http1 getGlobal receive %s\n", msg.NodeID)
	server.node.MsgGlobal <- &msg
}

func (server *Server) getGlobalToLocal(writer http.ResponseWriter, request *http.Request) {
	var msg consensus.LocalMsg
	err := json.NewDecoder(request.Body).Decode(&msg)
	if err != nil {
		fmt.Println(err)
		return
	}
	// fmt.Printf("http2 getGlobalToLocal receive %s\n", msg.NodeID)
	server.node.MsgGlobal <- &msg
}

func send(url string, msg []byte) {
	buff := bytes.NewBuffer(msg)
	http.Post("http://"+url, "application/json", buff)
}
