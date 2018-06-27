/*

Minimalist server that coordinates peer-2-peer connections.

Usage:

$ go run server.go

*/

package main

import (
	"encoding/gob"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/rpc"
	"os"
	"sync"
	"time"
)

// Errors that the server could return.
type UnknownKeyError error

type KeyAlreadyRegisteredError string

func (e KeyAlreadyRegisteredError) Error() string {
	return fmt.Sprintf("server: key already registered [%s]", string(e))
}

type AddressAlreadyRegisteredError string

func (e AddressAlreadyRegisteredError) Error() string {
	return fmt.Sprintf("server: address already registered [%s]", string(e))
}

type ClientNameAlreadyRegisteredError string

func (e ClientNameAlreadyRegisteredError) Error() string {
	return fmt.Sprintf("server: client name already registered [%s]", string(e))
}

type MyServer int

type RegisterInfo struct {
	ClientName      string
	PublicKey       string
	FileName        string
	IPPort          string
	RecentHeartbeat int64
}

var (
	unknownKeyError UnknownKeyError = errors.New("server: unknown key")
	errLog          *log.Logger     = log.New(os.Stderr, "[serv] ", log.Lshortfile|log.LUTC|log.Lmicroseconds)
	outLog          *log.Logger     = log.New(os.Stderr, "[serv] ", log.Lshortfile|log.LUTC|log.Lmicroseconds)

	check checkClients = checkClients{checkClientName: make(map[string]bool),
		checkIPPort: make(map[string]bool), checkPublicKey: make(map[string]bool),
		registeredClients: make(map[string]RegisterInfo)}
	knownFiles map[string][]string // filename: clients

)

type checkClients struct {
	sync.RWMutex
	checkClientName   map[string]bool
	checkPublicKey    map[string]bool
	checkIPPort       map[string]bool
	registeredClients map[string]RegisterInfo // registered clients
}

// Interface between server and clients
type ServerInterface interface {
	RegisterClient(client RegisterInfo, reply *bool) error
	RetrieveNeighbours(fileName string, reply *[]RegisterInfo) error
	HeartBeat(clientName string, _ignored *bool) error
	IsAlive(clientName string, reply *bool) error
}

func registerServer(server *rpc.Server, s ServerInterface) {
	server.RegisterName("ServerRPC", s)
}

// Parses args, setups up RPC server.
func main() {
	gob.Register(&net.TCPAddr{})

	knownFiles = make(map[string][]string)
	rand.Seed(time.Now().UnixNano())

	args := os.Args[1:]
	serverIP := args[0]
	ServerInterface := new(MyServer)
	serverRPC := rpc.NewServer()
	registerServer(serverRPC, ServerInterface)
	l, e := net.Listen("tcp", serverIP)
	if e != nil {
		log.Fatal("server error:", e)
		return
	}

	for {
		conn, _ := l.Accept()
		go serverRPC.ServeConn(conn)
	}
}

func (s *MyServer) RegisterClient(client RegisterInfo, reply *bool) error {
	check.Lock()
	defer check.Unlock()
	if check.checkClientName[client.ClientName] {
		return ClientNameAlreadyRegisteredError(client.ClientName)
	}
	if check.checkPublicKey[client.PublicKey] {
		return KeyAlreadyRegisteredError(client.PublicKey)
	}
	if check.checkIPPort[client.IPPort] {
		return AddressAlreadyRegisteredError(client.IPPort)
	}
	check.checkClientName[client.ClientName] = true
	check.checkPublicKey[client.PublicKey] = true
	check.checkIPPort[client.IPPort] = true
	if len(knownFiles[client.FileName]) == 0 {
		knownFiles[client.FileName] = make([]string, 0)
	}
	x := knownFiles[client.FileName]
	x = append(x, client.ClientName)
	knownFiles[client.FileName] = x

	check.registeredClients[client.ClientName] = RegisterInfo{client.ClientName,
		client.PublicKey, client.FileName, client.IPPort, time.Now().UnixNano()}

	fmt.Println(knownFiles[client.FileName], check.registeredClients[client.ClientName])

	go monitor(client.ClientName, time.Duration(2000)*time.Millisecond) // @set check time

	outLog.Printf("Got Register from %s\n", client.IPPort)
	*reply = true
	return nil
}

// Function to delete dead miners (no recent heartbeat)
func monitor(k string, heartBeatInterval time.Duration) {
	for {
		check.Lock()
		if time.Now().UnixNano()-check.registeredClients[k].RecentHeartbeat > int64(heartBeatInterval) {
			outLog.Printf("%s timed out\n", k, check.registeredClients[k].ClientName, check.registeredClients[k].IPPort)
			file := check.registeredClients[k].FileName
			ip := check.registeredClients[k].IPPort
			publickey := check.registeredClients[k].PublicKey
			fmt.Println("del:", knownFiles[file], check.checkClientName, check.checkIPPort)
			delete(check.registeredClients, k)
			delete(check.checkClientName, k)
			delete(check.checkIPPort, ip)
			delete(check.checkPublicKey, publickey)
			arr := knownFiles[file]
			for i := 0; i < len(arr); i++ {
				if arr[i] == k {
					arr = append(arr[:i], arr[i+1:]...)
				}
			}
			knownFiles[file] = arr
			fmt.Println("delaft:", knownFiles[file], check.checkClientName, check.checkIPPort)
			check.Unlock()
			return
		}
		outLog.Printf("%s is alive\n", check.registeredClients[k].ClientName, check.registeredClients[k].IPPort)
		check.Unlock()
		time.Sleep(heartBeatInterval)
	}
}

func (s *MyServer) HeartBeat(clientName string, reply *bool) error {
	check.Lock()
	defer check.Unlock()

	if ok := check.checkClientName[clientName]; !ok {
		fmt.Println("unknown client:", clientName)
		return nil
		// return unknownClientError
	}

	*reply = true
	clt := check.registeredClients[clientName]
	clt.RecentHeartbeat = time.Now().UnixNano()
	check.registeredClients[clientName] = clt

	return nil
}

func (s *MyServer) IsAlive(clientName string, reply *bool) error {
	if check.checkClientName[clientName] {
		*reply = true
	}
	return nil
}

func (s *MyServer) RetrieveNeighbours(fileName string, reply *[]RegisterInfo) error {
	clients := knownFiles[fileName]
	for i := 0; i < len(clients); i++ {
		*reply = append(*reply, check.registeredClients[clients[i]])
	}
	return nil
}
