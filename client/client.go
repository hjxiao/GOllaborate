package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	mathRand "math/rand"
	"net"
	"net/rpc"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

/*
	Usage:
	go run client.go [server ip:port] [client IP:port] [Listen to editor IP:port]
*/

type FakeAuthorError string

func (e FakeAuthorError) Error() string {
	return fmt.Sprintf("The Author is faked [%s]", string(e))
}

/***************
Global Variables
****************/
var (
	data              InitialDoc
	connServer        *rpc.Client    // server connection
	connToEditor      *rpc.Client    // connection to editor
	collaborators     []RegisterInfo // a list of information of neighbour nodes
	documentName      string
	myName            string // Author
	privKey           *rsa.PrivateKey
	myPublicKeyString string
	Opcount           int
	sendingBuffer     *Queue
	hasDocument       bool
	neighbours        map[string]neighbourInfo //  a map of all neighbour client nodes we have connected
)

/*******
Structs
********/
/* Represents a single character with information necessary to
   display it in the terminal text editor and send it to neighbours */
type charObj struct {
	Author     string
	CharValue  rune
	EditorPosX int
	EditorPosY int
	CRDTPos    []int
	Opcount    int
}

/* RPCcharObj is the form of the charObj that we will package and send
   to neighbours. This represents a newly inserted or deleted character. */
type RPCcharObj struct {
	Author    string
	RPCauthor string
	CharValue rune
	CRDTPos   []int
	Opcount   int    // Num of operation for this author to solve out of order message
	Type      string // either insertion or deletion
	Hash      []byte
	TTL       int
}

// Queue is a basic FIFO queue based on a circular list that resizes as needed.
type Queue struct {
	nodes []*RPCcharObj
	size  int
	head  int
	tail  int
	count int
}

type InitialDoc struct {
	Doc      []charObj
	CountMap map[AuthorAndCount]State
}

type State int

const (
	S1 State = iota // I have not seen character with opCount X from author
	S2              // I have seen said character
	S3              // I have deleted said character
	S4              // I have not seen character yet, but I need to delete it
)

type AuthorAndCount struct {
	Author string
	Count  int
}

/**************************************
Public Interfaces
***************************************/
// Interface between 2 clients
type ClientCommunicationInterface interface {
	SendCRDT(char RPCcharObj, reply *bool) error
	ConnectToClient(info RegisterInfo, reply *bool) error
	RetrieveDoc(b bool, reply *InitialDoc) error
}

// Interface between 2 clients
type ClientCommunicationRPC int

// Interface between client and text editor app
type AppCommunicationInterface interface {
	SendCRDTEditorToClient(char RPCcharObj, reply *string) error
	SetConnection(editorInfo string, reply *bool) error
}

// Interface between client and text editor app
type AppCommunicationRPC int

type RegisterInfo struct {
	ClientName      string
	PublicKey       string
	FileName        string
	IPPort          string
	RecentHeartbeat int64
}

type neighbourInfo struct {
	PublicKey *rsa.PublicKey
	IPPort    string
	conn      *rpc.Client
}

/**********************
Entry point into client
***********************/
func main() {
	// A new client instance initially assumes it does not have a (copy of) document
	hasDocument = false
	data = InitialDoc{CountMap: nil, Doc: nil}
	// Read in command line args
	// args[0] is server:port, args[1] is clientIP:port, args[2] is editorIP:port
	args := os.Args[1:]

	serverIP := args[0]
	ipPort := args[1]
	listenEditorIP := args[2] // can hard code this because editor and client will always be on same vm
	fmt.Println("IP & Port is: " + ipPort)

	e := listenToEditor(listenEditorIP)
	if e != nil {
		log.Fatal("listen to editor error:", e)
		return
	}

	for { // wait Editor to connect
		if myName != "" && documentName != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	err := listenToClients(ipPort) // listen to other client's RPC
	if err != nil {
		log.Fatal("client listen error:", err)
		return
	}

	connServer, err = registerInServer(ipPort, serverIP) // register client's information in server
	if err != nil {
		fmt.Println("Unable to connect to server")
		var x1, x2 int
		err = connToEditor.Call("EditorListenRPC.Quit", x1, &x2)
		return
	}

	go sendHeartBeat(ipPort, serverIP)

	// ask server for collaborators
	collaborators = make([]RegisterInfo, 0)
	err = connServer.Call("ServerRPC.RetrieveNeighbours", documentName, &collaborators)
	if err != nil {
		fmt.Println(err)
	}

	// Collaborators includes itself, hence look for a list >=2 to
	// retrieve existing document
	if len(collaborators) < 2 {
		fmt.Println("First to edit document; therefore no need to retrieve document")
		hasDocument = true
	}

	err = connectToNeighbours(ipPort)
	if err != nil {
		log.Fatal("connect neighbours", err)
		return
	}

	// If there are neighbours, then we need to retrieve a document from them
	fmt.Println("22222")
	fmt.Println(len(neighbours))
	if len(neighbours) > 0 {
		fmt.Println("Retrieving doc")
		err = retrieveDoc()
		if err != nil {
			fmt.Println(err)
		}
		fmt.Println("######")
		updateEditorCharObjList()
		hasDocument = true
	}
	//go checkBuffer()
	time.Sleep(50000 * time.Second)
}

func sendHeartBeat(myIP string, serverIP string) {
	for {
		var reply bool
		var err1 error

		err1 = connServer.Call("ServerRPC.HeartBeat", myName, &reply)

		time.Sleep(1 * time.Second)
		if err1 != nil {
			fmt.Println("error:", err1)
		}
		if !reply {

			for {
				fmt.Println(" reconnect server")
				var success bool
				connServer, success = reconnect(myIP, serverIP) // register client's information in server
				if success {
					fmt.Println(" reconnect server successful")
					break
				}

				time.Sleep(1 * time.Second)
			}
		}

	}
}

func registerInServer(myIP string, serverIP string) (*rpc.Client, error) {
	privKey, _ = rsa.GenerateKey(rand.Reader, 1024) // generate private public key pair
	myPublicKeyString = convertPublicKeyToStr(&privKey.PublicKey)
	var err error
	fmt.Println("my key is: " + myPublicKeyString)
	info := &RegisterInfo{myName, myPublicKeyString, documentName, myIP, 0}
	connServer, err = rpc.Dial("tcp", serverIP)
	if err != nil {
		return nil, err
	}
	var reply bool
	err = connServer.Call("ServerRPC.RegisterClient", info, &reply)

	return connServer, err
}

func reconnect(myIP string, serverIP string) (*rpc.Client, bool) {
	var err error
	connServer, err = rpc.Dial("tcp", serverIP)
	if err != nil {
		return nil, false
	}
	var reply bool
	info := &RegisterInfo{myName, myPublicKeyString, documentName, myIP, 0}
	err2 := connServer.Call("ServerRPC.RegisterClient", info, &reply)
	if err2 != nil {
		return nil, false
	}
	return connServer, true
}

func listenToClients(myIP string) error {
	ClientCommunicationInterface := new(ClientCommunicationRPC)
	clientRPC := rpc.NewServer()
	registerServer(clientRPC, ClientCommunicationInterface)
	l, err := net.Listen("tcp", myIP) // listen other client's rpc call
	go clientRPC.Accept(l)
	runtime.Gosched()
	return err
}

// client connect to its neighbours
func connectToNeighbours(myIP string) error {
	neighbours = make(map[string]neighbourInfo)
	var err error
	var conn *rpc.Client
	var reply bool
	myInfo := RegisterInfo{myName, myPublicKeyString, documentName, myIP, 0}
	for i := 0; i < len(collaborators) && len(collaborators) > 1; i++ {
		if collaborators[i].ClientName == myName {
			continue
		}
		conn, err = rpc.Dial("tcp", collaborators[i].IPPort) // connect to other clients
		if err != nil {
			return err
		}
		fmt.Println("@@@Connect to other person & add their name to my editor")
		err = connToEditor.Call("EditorListenRPC.UpdateNeighbours", collaborators[i].ClientName, &reply)
		if err != nil {
			return err
		}
		fmt.Println("@@@Add name complete")
		pk, _ := convertStrToPublicKey(collaborators[i].PublicKey)
		neighbours[collaborators[i].ClientName] = neighbourInfo{pk, collaborators[i].IPPort, conn}
		var reply bool

		err = conn.Call("ClientCommunicationRPC.ConnectToClient", myInfo, &reply)
		if err != nil {
			return err
		}
	}
	fmt.Println("nebs", neighbours)
	return nil
}

// pkcs1PublicKey reflects the ASN.1 structure of a PKCS#1 public key.
type pkcs1PublicKey struct {
	N *big.Int
	E int
}

// MarshalPKCS1PublicKey converts an RSA public key to PKCS#1, ASN.1 DER form.
func MarshalPKCS1PublicKey(key *rsa.PublicKey) []byte {
	derBytes, _ := asn1.Marshal(pkcs1PublicKey{
		N: key.N,
		E: key.E,
	})
	return derBytes
}
func convertPublicKeyToStr(key *rsa.PublicKey) string {
	keyBytes := MarshalPKCS1PublicKey(key)
	keyInString := hex.EncodeToString(keyBytes)
	return keyInString
}

func listenToEditor(listenEditorIP string) error {
	AppCommunicationInterface := new(AppCommunicationRPC)
	clientEditorRPC := rpc.NewServer()
	registerEditorRPC(clientEditorRPC, AppCommunicationInterface)
	l, e := net.Listen("tcp", listenEditorIP) // listen for editor

	go clientEditorRPC.Accept(l)
	runtime.Gosched()
	return e
}

func (s AppCommunicationRPC) SendCRDTEditorToClient(char RPCcharObj, reply *string) error {
	fmt.Println("get char from editor, send editor RPC", char)
	ttl := char.TTL
	char.TTL = 0
	data := []byte(fmt.Sprintf("%v", char))

	hashed, _ := rsa.SignPKCS1v15(rand.Reader, privKey, 0, data)
	char.Hash = hashed
	char.TTL = ttl
	for name, v := range neighbours {
		go sendChar(name, char, v.conn)
	}
	return nil
}

func (s AppCommunicationRPC) SetConnection(editorInfo string, reply *bool) error {
	sendingBuffer = NewQueue(1)

	var err error
	args := strings.Split(editorInfo, "|")
	myName = args[1]
	documentName = args[2]
	connToEditor, err = rpc.Dial("tcp", args[0]) // connect to editor
	fmt.Println("Received connection from editor", myName)
	if err != nil {
		log.Fatal("editor dialing:", err)
	}
	return err
}
func registerServer(server *rpc.Server, s ClientCommunicationInterface) {
	// registers interface by name of `MyServer`.
	server.RegisterName("ClientCommunicationRPC", s)
}

func registerEditorRPC(server *rpc.Server, s AppCommunicationInterface) {
	// registers interface by name of `MyServer`.
	server.RegisterName("EditorRPC", s)
}

func sendChar(client string, sendobj RPCcharObj, neighbour *rpc.Client) {
	var reply bool
	for !reply {
		var reply2 bool
		err := neighbour.Call("ClientCommunicationRPC.SendCRDT", sendobj, &reply)
		if err != nil {
			fmt.Println(err)
			errStr := fmt.Sprintf("%v", err)
			ss := fmt.Sprintf("%v", FakeAuthorError(""))
			if ss == errStr {
				// fmt.Println("+++rr")
				return
			}else{
				_ = connToEditor.Call("EditorListenRPC.RemoveNeighbour", client, &reply2)
				delete(neighbours, client)
				fmt.Println("server down delete neighbour", client, neighbours[client])
				return
			}
		}
		if !reply {
			var online bool
		
			err := connServer.Call("ServerRPC.IsAlive", client, &online)
			if err !=nil{
				_ = connToEditor.Call("EditorListenRPC.RemoveNeighbour", client, &reply2)
				delete(neighbours, client)
				fmt.Println("server down delete neighbour", client, neighbours[client])
				return
			}
			if !online {				
				delete(neighbours, client)
				_ = connToEditor.Call("EditorListenRPC.RemoveNeighbour", client, &reply2)
				fmt.Println("delete neighbour", client, neighbours[client])
				return
			}
			//// ask server, if not online then delete it from neighbours, break
			/// otherwise redo
			fmt.Println("sending not successful")

		}
		fmt.Println("sending", reply)
		time.Sleep(1000 * time.Millisecond)
	}
}

func retrieveDoc() error {
	b := false
	var err error
	for _, v := range neighbours {
		mathRand.Seed(time.Now().UnixNano())
		n := mathRand.Intn(len(neighbours))
		fmt.Printf("Index: %d\n", n)
		fmt.Printf("Num neighbours: %d\n", len(neighbours))
		err = v.conn.Call("ClientCommunicationRPC.RetrieveDoc", b, &data)
		if err != nil {
			return err
		}
		// fmt.Println("The doc we grabbed from neighbour:")
		// fmt.Println(doc)
		if data.CountMap != nil && data.Doc != nil {
			fmt.Println("Retrieve successful")
			break
		}
	}
	fmt.Println("Exited retrieveDoc for-loop")
	return nil
}

func updateEditorCharObjList() {
	b := false
	fmt.Println("Inside updateEditorCharObjList")
	_ = connToEditor.Call("EditorListenRPC.UpdateEditorDoc", data, &b)
	fmt.Printf("RPC call to editor to update result: %s\n", strconv.FormatBool(b))
	fmt.Println("Checking local editor updated its char list")
	var testCharList InitialDoc
	_ = connToEditor.Call("EditorListenRPC.GetDocFromEditor", true, &testCharList)
	fmt.Println(testCharList.Doc)
}

func (t *ClientCommunicationRPC) SendCRDT(char RPCcharObj, reply *bool) error {
	fmt.Println("got author:", char.Author, char.RPCauthor, char.CharValue, char.CRDTPos, char.TTL, char.Opcount)

	if neighbours[char.RPCauthor].PublicKey != nil {
		checkPK := neighbours[char.RPCauthor].PublicKey

		verifyReceivedChar(checkPK, char)
		sendSuccess := false
		//fmt.Println("Before SendCRDTtoEditor")
		err := connToEditor.Call("EditorListenRPC.SendCRDTtoEditor", &char, &sendSuccess)
		//fmt.Println("After SendCRDTtoEditor")
		if err != nil {
			return err
		}
		char.TTL--
		if char.TTL > 0 {
			for _, v := range neighbours {
				var reply2 bool
				v.conn.Call("ClientCommunicationRPC.SendCRDT", &char, &reply2)
			}
		}
		*reply = true
	} else if char.Author == myName {
		checkPK := privKey.PublicKey

		verifyReceivedChar(&checkPK, char)
		fmt.Println("Good2")
		char.TTL--
		if char.TTL > 0 {
			for _, v := range neighbours {
				var reply2 bool
				_ = v.conn.Call("ClientCommunicationRPC.SendCRDT", &char, &reply2)
			}
		}
		*reply = true
	}

	return nil
}

func verifyReceivedChar(checkPK *rsa.PublicKey, char RPCcharObj) error {
	hash := char.Hash
	ttl := char.TTL
	char.TTL = 0
	char.Hash = nil
	data := []byte(fmt.Sprintf("%v", char))
	if rsa.VerifyPKCS1v15(checkPK, 0, data, hash) != nil {
		fmt.Printf("Error:Faked Author")
		return FakeAuthorError("")
	}
	fmt.Println("Good")
	char.Hash = hash
	char.TTL = ttl
	return nil
}

// This RPC call establishes the reverse RPC connection. I.e. client 1 directly dials
// client 2, and then client 1 calls this function so that client 2 will dial client 1
func (t *ClientCommunicationRPC) ConnectToClient(info RegisterInfo, reply *bool) error {
	if neighbours[info.ClientName].PublicKey != nil {
		fmt.Println("Already registered client:", info.ClientName)
	}
	conn, err := rpc.Dial("tcp", info.IPPort)
	if err != nil {
		fmt.Println("connect client error", err)
	}
	fmt.Println("###Other person connected to me. I add name to my editor")
	var r bool
	nameAddToEditor := info.ClientName
	err = connToEditor.Call("EditorListenRPC.UpdateNeighbours", nameAddToEditor, &r)
	if err != nil {
		return err
	}
	fmt.Println("###Adding name is done")
	pk, _ := convertStrToPublicKey(info.PublicKey)
	neighbours[info.ClientName] = neighbourInfo{pk, info.IPPort, conn}
	fmt.Println("connected client", neighbours[info.ClientName])
	return nil
}

// b is a dummy variable to satisfy golang RPC call interface
func (t *ClientCommunicationRPC) RetrieveDoc(b bool, reply *InitialDoc) error {
	fmt.Println("@@@@@")
	fmt.Println("Inside Client RetrieveDoc RPC call")
	if !hasDocument {
		*reply = InitialDoc{CountMap: nil, Doc: nil}
	} else {
		var docFromMyEditor InitialDoc
		_ = connToEditor.Call("EditorListenRPC.GetDocFromEditor", true, &docFromMyEditor)
		*reply = docFromMyEditor
		// fmt.Println("Doc from my editor")
		// fmt.Println(docFromMyEditor)
	}
	fmt.Println("Completed RetrieveDoc RPC")
	return nil
}

// NewQueue returns a new queue with the given initial size.
func NewQueue(size int) *Queue {
	return &Queue{
		nodes: make([]*RPCcharObj, size),
		size:  size,
	}
}

// Push adds a node to the queue.
func (q *Queue) Push(n *RPCcharObj) {
	if q.head == q.tail && q.count > 0 {
		nodes := make([]*RPCcharObj, len(q.nodes)+q.size)
		copy(nodes, q.nodes[q.head:])
		copy(nodes[len(q.nodes)-q.head:], q.nodes[:q.head])
		q.head = 0
		q.tail = len(q.nodes)
		q.nodes = nodes
	}
	q.nodes[q.tail] = n
	q.tail = (q.tail + 1) % len(q.nodes)
	q.count++
}

// Pop removes and returns a node from the queue in first to last order.
func (q *Queue) Pop() *RPCcharObj {
	if q.count == 0 {
		return nil
	}
	node := q.nodes[q.head]
	q.head = (q.head + 1) % len(q.nodes)
	q.count--
	return node
}

// It converts a string into a *rsa.PublicKey
func convertStrToPublicKey(keyStr string) (*rsa.PublicKey, error) {
	keyAsBytes, _ := hex.DecodeString(keyStr)
	key, err := ParsePKCS1PublicKey(keyAsBytes)
	return key, err
}

// ParsePKCS1PublicKey parses a PKCS#1 public key in ASN.1 DER form.
func ParsePKCS1PublicKey(der []byte) (*rsa.PublicKey, error) {
	var pub pkcs1PublicKey
	rest, err := asn1.Unmarshal(der, &pub)
	if err != nil {
		return nil, err
	}
	if len(rest) > 0 {
		return nil, asn1.SyntaxError{Msg: "trailing data"}
	}

	if pub.N.Sign() <= 0 || pub.E <= 0 {
		return nil, errors.New("x509: public key contains zero or negative value")
	}
	if pub.E > 1<<31-1 {
		return nil, errors.New("x509: public key contains large public exponent")
	}

	return &rsa.PublicKey{
		E: pub.E,
		N: pub.N,
	}, nil
}
