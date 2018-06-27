package main

import (
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/jroimartin/gocui"
)

var (
	userName        string
	neighbours      []string = make([]string, 0)
	gGlobal         *gocui.Gui
	charObjList     []charObj = make([]charObj, 0)
	Opcount         int
	conn            *rpc.Client // client editor RPC
	receivingBuffer *Queue
	authorCountMap  map[AuthorAndCount]State
	deletionBuffer  []RPCcharObj = make([]RPCcharObj, 0)
	wg              sync.WaitGroup
	done            = make(chan struct{})
	ctr             = 0
	receivedChar    RPCcharObj
)

type State int

const (
	S1 State = iota // I have not seen character with opCount X from author
	S2              // I have seen said character
	S3              // I have deleted said character
	S4              // I have not seen character yet, but I need to delete it
)

const NumGoroutines = 10
const crdtInterval = 10

type myEditor struct {
}

type AuthorAndCount struct {
	Author string
	Count  int
}

type InitialDoc struct {
	Doc      []charObj
	CountMap map[AuthorAndCount]State
}

type charObj struct {
	Author     string
	CharValue  rune
	EditorPosX int
	EditorPosY int
	CRDTPos    []int
	Opcount    int
}

type RPCcharObj struct {
	Author    string
	RPCauthor string
	CharValue rune
	CRDTPos   []int
	Opcount   int    //No of operation for this author to solve out of order message
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

func insertLocal(value rune, v *gocui.View) {
	newRPCchar := RPCcharObj{Author: userName, RPCauthor: userName, CharValue: value, Type: "insertion", Hash: nil, TTL: 2}
	newChar := charObj{Author: userName, CharValue: value}
	newCRDT := make([]int, 0)
	listLength := len(charObjList)
	var reply string
	// if charObjList is empty, create first Obj
	if listLength == 0 {
		newCRDT = append(newCRDT, 0)
		newChar.CRDTPos = newCRDT
		newChar.EditorPosX = 0
		newChar.EditorPosY = 0
		Opcount++
		newChar.Opcount = Opcount
		charObjList = append(charObjList, newChar)
		setMapState(newChar.Author, newChar.Opcount, S2)
		newRPCchar.Opcount = Opcount
		newRPCchar.CRDTPos = newCRDT
		_ = conn.Call("EditorRPC.SendCRDTEditorToClient", &newRPCchar, &reply)
		return
	}
	cx, cy := v.Cursor()
	newChar.EditorPosX = cx
	newChar.EditorPosY = cy
	var k int
	for k = range charObjList {
		privObj := charObjList[k]
		// have cursor at very beginning
		if cx == 0 && cy == 0 {
			newCRDT = append(newCRDT, privObj.CRDTPos[0]-crdtInterval)
			newChar.CRDTPos = newCRDT
			newChar.Opcount = Opcount + 1
			charObjList = append([]charObj{newChar}, charObjList...)
			setMapState(newChar.Author, newChar.Opcount, S2)
			for i := k + 1; i < len(charObjList); i++ {
				if charObjList[i].EditorPosY == newChar.EditorPosY {
					charObjList[i].EditorPosX++
				}
			}
			break
		} else if cx > privObj.EditorPosX+1 || cy != privObj.EditorPosY {
			if charObjList[listLength-1].CharValue == 0 && cy == charObjList[listLength-1].EditorPosY+1 && cx == 0 {
				// reaching cursor followed by enter
				privObj = charObjList[listLength-1]
				privCRDTValue := privObj.CRDTPos[0]
				newCRDT = append(newCRDT, privCRDTValue+crdtInterval)
				newChar.Opcount = Opcount + 1
				newChar.CRDTPos = newCRDT
				charObjList = append(charObjList, newChar)
				setMapState(newChar.Author, newChar.Opcount, S2)
				break
			} else if cy == charObjList[k+1].EditorPosY && cx == 0 {
				nextObj := charObjList[k+1]
				//insert newChar at k+1 (figure out crdt from privObj and nextObj)
				newCRDT = generateCRDTBetween(privObj, nextObj)
				newChar.Opcount = Opcount + 1
				newChar.CRDTPos = newCRDT
				nextList := append([]charObj{}, charObjList[k+1:]...)
				charObjList = append(charObjList[0:k+1], newChar)
				charObjList = append(charObjList, nextList...)
				setMapState(newChar.Author, newChar.Opcount, S2)
				if value != 0 {
					// if not enter, move all subsequent line's Obj one space further (posxX + 1)
					for i := k + 2; i < len(charObjList); i++ {
						if charObjList[i].EditorPosY == newChar.EditorPosY {
							charObjList[i].EditorPosX++
						}
					}
					// if value is enter, update following obj's x and y
				} else if value == 0 {
					newX := 0 //new X for object followed enter on the same line
					for i := k + 2; i < len(charObjList); i++ {
						if charObjList[i].EditorPosY == newChar.EditorPosY {
							charObjList[i].EditorPosX = newX
							newX++
						}
						charObjList[i].EditorPosY++
					}
				}
				break
			}
		} else {
			//reaching cursor
			if k+1 == listLength {
				// cursor is at the end of list
				privCRDTValue := privObj.CRDTPos[0]
				newCRDT = append(newCRDT, privCRDTValue+crdtInterval)
				newChar.Opcount = Opcount + 1
				newChar.CRDTPos = newCRDT
				charObjList = append(charObjList, newChar)
				setMapState(newChar.Author, newChar.Opcount, S2)
				break
			} else {
				// cursor is not at the end of list
				nextObj := charObjList[k+1]
				newChar.Opcount = Opcount + 1
				//insert newChar at k+1 (figure out crdt from privObj and nextObj)
				newChar.CRDTPos = generateCRDTBetween(privObj, nextObj)
				newCRDT = newChar.CRDTPos
				nextList := append([]charObj{}, charObjList[k+1:]...)
				charObjList = append(charObjList[0:k+1], newChar)
				charObjList = append(charObjList, nextList...)
				setMapState(newChar.Author, newChar.Opcount, S2)
				if value != 0 {
					// if not enter, move all subsequent line's Obj one space further (posxX + 1)
					for i := k + 2; i < len(charObjList); i++ {
						if charObjList[i].EditorPosY == newChar.EditorPosY {
							charObjList[i].EditorPosX++
						}
					}
					// if value is enter, update following obj's x and y
				} else if value == 0 {
					newX := 0 //new X for object followed enter on the same line
					for i := k + 2; i < len(charObjList); i++ {
						if charObjList[i].EditorPosY == newChar.EditorPosY {
							charObjList[i].EditorPosX = newX
							newX++
						}
						charObjList[i].EditorPosY++
					}
				}
				break
			}
		}
	}
	Opcount++
	newRPCchar.Opcount = Opcount
	newRPCchar.CRDTPos = newCRDT
	_ = conn.Call("EditorRPC.SendCRDTEditorToClient", &newRPCchar, &reply)
}

func generateCRDTBetween(priv charObj, next charObj) []int {
	var sameLevel bool
	privLength := len(priv.CRDTPos)
	nextLength := len(next.CRDTPos)
	newCRDT := make([]int, privLength)
	copy(newCRDT, priv.CRDTPos)
	// check if next and priv is at same level
	if privLength == nextLength {
		sameLevel = true
	} else {
		sameLevel = false
	}
	privCRDTValue := priv.CRDTPos[privLength-1]
	nextCRDTValue := next.CRDTPos[nextLength-1]
	if sameLevel {
		if nextCRDTValue-privCRDTValue > 1 {
			newCRDTValue := (nextCRDTValue + privCRDTValue) / 2
			newCRDT[privLength-1] = newCRDTValue
		} else {
			// start a new crdt level
			newCRDT = append(newCRDT, 0)
		}
		// not same level
	} else {
		if privLength > nextLength {
			newCRDT[privLength-1] = privCRDTValue + crdtInterval
		} else {
			copy(newCRDT, next.CRDTPos)
			newCRDT = append(newCRDT, nextCRDTValue-crdtInterval)
		}
	}
	return newCRDT
}

func deleteLocal(v *gocui.View) {
	var k int
	var nextX int
	enterRemove := false
	var reply string
	cx, cy := v.Cursor()
	newRPCchar := RPCcharObj{RPCauthor: userName, Type: "deletion", Hash: nil, TTL: 2}
	// do nothing if cursor is at beginning
	if cx == 0 && cy == 0 {
		return
	}
	// remove charObj from charObjList
	for k = range charObjList {
		if (cy == charObjList[k].EditorPosY && cx == charObjList[k].EditorPosX+1) || (charObjList[k].CharValue == 0 && charObjList[k].EditorPosY+1 == cy && cx == 0) {
			newRPCchar.Author = charObjList[k].Author
			newRPCchar.CharValue = charObjList[k].CharValue
			if charObjList[k].CharValue == 0 {
				enterRemove = true
				nextX = charObjList[k].EditorPosX
			}
			newRPCchar.Opcount = charObjList[k].Opcount
			newRPCchar.CRDTPos = charObjList[k].CRDTPos
			_ = conn.Call("EditorRPC.SendCRDTEditorToClient", &newRPCchar, &reply)
			charObjList = append(charObjList[:k], charObjList[k+1:]...)
			setMapState(newRPCchar.Author, newRPCchar.Opcount, S3)
			break
		}
	}
	// update subsequent charObj'x
	if !enterRemove {
		for k = range charObjList {
			if cy == charObjList[k].EditorPosY && cx < charObjList[k].EditorPosX+1 {
				charObjList[k].EditorPosX--
			}
		}
	} else {
		for k = range charObjList {
			if cy == charObjList[k].EditorPosY && cx < charObjList[k].EditorPosX+1 {
				charObjList[k].EditorPosX = nextX + charObjList[k].EditorPosX
				charObjList[k].EditorPosY--
			} else if cy < charObjList[k].EditorPosY {
				charObjList[k].EditorPosY--
			}
		}
	}
}

func quit(g *gocui.Gui, v *gocui.View) error {
	close(done)
	return gocui.ErrQuit
}

func printcharObjList(g *gocui.Gui, v *gocui.View) error {
	cx, cy := v.Cursor()
	fmt.Printf("cx=%d, cy=%d, len=%d %v\n", cx, cy, len(charObjList), charObjList)
	return nil
}

func (ve *myEditor) Edit(v *gocui.View, key gocui.Key, ch rune, mod gocui.Modifier) {
	switch {
	case ch != 0 && mod == 0:
		insertLocal(ch, v)
		v.EditWrite(ch)
	case key == gocui.KeySpace:
		space := 32
		ch := rune(space)
		insertLocal(ch, v)
		v.EditWrite(ch)
	case key == gocui.KeyBackspace || key == gocui.KeyBackspace2:
		deleteLocal(v)
		v.EditDelete(true)
	case key == gocui.KeyEnter:
		insertLocal(ch, v)
		v.EditNewLine()
	case key == gocui.KeyArrowDown:
		v.MoveCursor(0, 1, false)
	case key == gocui.KeyArrowUp:
		v.MoveCursor(0, -1, false)
	case key == gocui.KeyArrowLeft:
		v.MoveCursor(-1, 0, false)
	case key == gocui.KeyArrowRight:
		v.MoveCursor(1, 0, false)
	}
}

func layout(g *gocui.Gui) error {
	maxX, maxY := g.Size()
	if v, err := g.SetView("side", -1, -1, 10, maxY); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
		v.SelFgColor = gocui.ColorBlack
		fmt.Fprintln(v, userName)
	}
	if v, err := g.SetView("main", 10, -1, maxX, maxY); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Editable = true
		v.Wrap = true
		v.Editor = &myEditor{}
		if _, err := g.SetCurrentView("main"); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	args := os.Args[1:]
	userName = args[0]
	ipPort := args[1]   // RPC to client
	listento := args[2] // listen to ip port that client call it
	fileName := args[3]

	g, err := gocui.NewGui(gocui.OutputNormal)
	gGlobal = g
	if err != nil {
		log.Fatalln(err)
	}
	defer g.Close()

	g.SetManagerFunc(layout)
	g.InputEsc = true
	g.Cursor = true
	//g.Mouse = true

	conn, err = rpc.Dial("tcp", ipPort) // connect to client
	if err != nil {
		log.Fatal("dialing:", err)
		return
	}
	receivingBuffer = NewQueue(1)

	authorCountMap = make(map[AuthorAndCount]State)
	editorRPC(listento)
	var reply bool
	editorInfo := listento + "|" + userName + "|" + fileName
	_ = conn.Call("EditorRPC.SetConnection", &editorInfo, &reply)
	// go updateSideViewGoRoutine()
	// runtime.Gosched()

	if err := g.SetKeybinding("", gocui.KeyF1, gocui.ModNone, quit); err != nil {
		log.Fatalln(err)
	}

	if err := g.SetKeybinding("", gocui.KeyF2, gocui.ModNone, printcharObjList); err != nil {
		log.Fatalln(err)
	}

	if err := g.SetKeybinding("", gocui.KeyF7, gocui.ModNone, printNeighbours); err != nil {
		log.Fatalln(err)
	}

	if err := g.MainLoop(); err != nil && err != gocui.ErrQuit {
		log.Fatalln(err)
	}

	//go checkReceivingBuffer(g)

	//wg.Wait()
}

func testReceive(g *gocui.Gui, v *gocui.View) error {
	received := receivingBuffer.Pop()
	if received.Type == "insertion" {
		insertRemote(received, v)
	} else {
		deleteRemote(received, v)
	}
	return nil
}

func startCheck(g *gocui.Gui, v *gocui.View) error {
	go checkReceivingBuffer(v)
	return nil
}

func checkReceivingBuffer(v *gocui.View) error {
	for {
		received := receivingBuffer.Pop()
		if received != nil {
			if received.Type == "insertion" {
				insertRemote(received, v)
			} else {
				deleteRemote(received, v)
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil
}

func insertRemote(received *RPCcharObj, v *gocui.View) {
	//fmt.Println(received)
	var k, j int
	receivedCount := AuthorAndCount{Author: received.Author, Count: received.Opcount}
	// discard received obj if opcount is less than count saved in authorCountMap
	if authorCountMap[receivedCount] == S2 || authorCountMap[receivedCount] == S3 {
		// We have already inserted this character, or we had already deleted
		// this character when the insertion arrives late from elsewhere
		//println("count and author exist")
		return
	}
	if authorCountMap[receivedCount] == S4 {
		// need to remove deletion from buffer and silently drop CRDT
		authorCountMap[receivedCount] = S3
		deleteRPCFromDelBuf(*received)
		return
	}
	authorCountMap[receivedCount] = S2
	newChar := charObj{Author: received.Author, CharValue: received.CharValue, CRDTPos: received.CRDTPos, Opcount: received.Opcount}
	listLength := len(charObjList)
	// if charObjList is empty, create first obj
	if listLength == 0 {
		newChar.EditorPosX = 0
		newChar.EditorPosY = 0
		charObjList = append(charObjList, newChar)
		insertChar(v, received.CharValue, newChar)
		return
	}
	// if received obj is at the end of charObjList
	if charObjList[listLength-1].CRDTPos[0] < received.CRDTPos[0] {
		if charObjList[listLength-1].CharValue != 0 {
			newChar.EditorPosX = charObjList[listLength-1].EditorPosX + 1
			newChar.EditorPosY = charObjList[listLength-1].EditorPosY
		} else {
			newChar.EditorPosX = 0
			newChar.EditorPosY = charObjList[listLength-1].EditorPosY + 1
		}
		charObjList = append(charObjList, newChar)
		insertChar(v, received.CharValue, newChar)
		return
	}
	// if received obj is in the middle of charObjList
	newLevel := len(received.CRDTPos)
	for k = range charObjList {
		//fmt.Printf("k is %d  ", k)
		curCRDT := charObjList[k].CRDTPos
		oldLevel := len(curCRDT)
		var minLevel int
		if oldLevel == newLevel {
			if compareSlice(curCRDT, received.CRDTPos) {
				// compare author if crdt are identical
				if charObjList[k].Author == received.Author {
					// if same author, ordered by Opcount
					// insert after current obj
					newChar.EditorPosX = charObjList[k+1].EditorPosX
					newChar.EditorPosY = charObjList[k+1].EditorPosY
					nextList := append([]charObj{}, charObjList[k+1:]...)
					charObjList = append(charObjList[0:k+1], newChar)
					charObjList = append(charObjList, nextList...)
					insertChar(v, received.CharValue, newChar)
					return
				} else if userName > received.Author {
					//fmt.Printf("userName %s larger than  received.Author %s  ", userName, received.Author) // kk larger than aa
					newChar.CRDTPos = append(newChar.CRDTPos, -crdtInterval)
					// insert after current obj
					if k == listLength-1 {
						newChar.EditorPosX = charObjList[listLength-1].EditorPosX + 1
						newChar.EditorPosY = charObjList[listLength-1].EditorPosY
						charObjList = append(charObjList, newChar)
						insertChar(v, received.CharValue, newChar)
						return
					}
					newChar.EditorPosX = charObjList[k+1].EditorPosX
					newChar.EditorPosY = charObjList[k+1].EditorPosY
					nextList := append([]charObj{}, charObjList[k+1:]...)
					charObjList = append(charObjList[0:k+1], newChar)
					charObjList = append(charObjList, nextList...)
					insertChar(v, received.CharValue, newChar)
					return
				} else {
					charObjList[k].CRDTPos = append(charObjList[k].CRDTPos, -crdtInterval)
					// insert at current obj
					newChar.EditorPosX = charObjList[k].EditorPosX
					newChar.EditorPosY = charObjList[k].EditorPosY
					nextList := append([]charObj{}, charObjList[k:]...)
					charObjList = append(charObjList[0:k], newChar)
					charObjList = append(charObjList, nextList...)
					insertChar(v, received.CharValue, newChar)
					return
				}
			} else {
				// same level different crdt
				for j = 0; j < oldLevel; j++ {
					if received.CRDTPos[j] < curCRDT[j] {
						newChar.EditorPosX = charObjList[k].EditorPosX
						newChar.EditorPosY = charObjList[k].EditorPosY
						nextList := append([]charObj{}, charObjList[k:]...)
						charObjList = append(charObjList[0:k], newChar)
						charObjList = append(charObjList, nextList...)
						insertChar(v, received.CharValue, newChar)
						return
					}
					if received.CRDTPos[j] > curCRDT[j] {
						j = oldLevel
					}
				}
			}
		} else {
			// different level
			if oldLevel < newLevel {
				minLevel = oldLevel
			} else {
				minLevel = newLevel
			}
			// compare each level
			for j = 0; j < minLevel; j++ {
				//fmt.Printf("at different level k is %d  ", k)
				if received.CRDTPos[j] < curCRDT[j] {
					newChar.EditorPosX = charObjList[k].EditorPosX
					newChar.EditorPosY = charObjList[k].EditorPosY
					nextList := append([]charObj{}, charObjList[k:]...)
					charObjList = append(charObjList[0:k], newChar)
					charObjList = append(charObjList, nextList...)
					//println("at right place")
					insertChar(v, received.CharValue, newChar)
					return
				}
				if received.CRDTPos[j] > curCRDT[j] {
					j = minLevel
				} else if j == minLevel-1 {
					if minLevel == newLevel {
						newChar.EditorPosX = charObjList[k].EditorPosX
						newChar.EditorPosY = charObjList[k].EditorPosY
						nextList := append([]charObj{}, charObjList[k:]...)
						charObjList = append(charObjList[0:k], newChar)
						charObjList = append(charObjList, nextList...)
						insertChar(v, received.CharValue, newChar)
						return
					}
				}
			}
		}
	}
}

// move cursor to desired cordinate, insert char, then move cursor back
func insertChar(v *gocui.View, value rune, newChar charObj) {
	cx, cy := v.Cursor()
	v.SetCursor(newChar.EditorPosX, newChar.EditorPosY)
	if value == 0 {
		v.EditNewLine()
	} else {
		v.EditWrite(value)
	}
	// move cursor back
	if value == 0 {
		if cy > newChar.EditorPosY {
			cy++
		} else if cy == newChar.EditorPosY && cx >= newChar.EditorPosX {
			cy++
			cx = cx - newChar.EditorPosX
		}
	} else {
		if cy == newChar.EditorPosY && cx >= newChar.EditorPosX {
			cx++
		}
	}
	v.SetCursor(cx, cy)
	// update following obj
	if value != 0 {
		// if not enter, move all subsequent line's Obj one space further (posxX + 1)
		for i := 0; i < len(charObjList); i++ {
			if charObjList[i].EditorPosY == newChar.EditorPosY && charObjList[i].EditorPosX >= newChar.EditorPosX {
				if !compareSlice(charObjList[i].CRDTPos, newChar.CRDTPos) {
					charObjList[i].EditorPosX++
				}
			}
		}
		// if value is enter, update following obj's x and y
	} else if value == 0 {
		newX := 0 //new X for object followed enter on the same line
		for i := 0; i < len(charObjList); i++ {
			if charObjList[i].EditorPosY == newChar.EditorPosY && charObjList[i].EditorPosX >= newChar.EditorPosX {
				if !compareSlice(charObjList[i].CRDTPos, newChar.CRDTPos) {
					charObjList[i].EditorPosX = newX
					newX++
					charObjList[i].EditorPosY++
				}
			} else if charObjList[i].EditorPosY > newChar.EditorPosY {
				charObjList[i].EditorPosY++
			}
		}
	}
}

func deleteRemote(received *RPCcharObj, v *gocui.View) {
	receivedCount := AuthorAndCount{Author: received.Author, Count: received.Opcount}
	// discard deletion CRDT if we've already seen it
	if authorCountMap[receivedCount] == S3 || authorCountMap[receivedCount] == S4 {
		// We have already seen this deletion CRDT
		return
	}
	if authorCountMap[receivedCount] == S1 {
		// Need to add deletion to deletion buffer
		deletionBuffer = append(deletionBuffer, *received)
		authorCountMap[receivedCount] = S4
		return
	}
	authorCountMap[receivedCount] = S3

	var k int
	var deletedX, deletedY int
	cx, cy := v.Cursor()
	// find charObj from charObjList
	for k = range charObjList {
		if compareSlice(charObjList[k].CRDTPos, received.CRDTPos) {
			deletedX = charObjList[k].EditorPosX
			deletedY = charObjList[k].EditorPosY
			if charObjList[k].CharValue == 0 {
				v.SetCursor(0, deletedY+1)
				v.EditDelete(true)
			} else {
				v.SetCursor(deletedX+1, deletedY)
				v.EditDelete(true)
			}
			// remove from list
			charObjList = append(charObjList[:k], charObjList[k+1:]...)
			break
		}
	}
	// set cursor back
	if received.CharValue == 0 {
		if cy > deletedY+1 {
			cy--
		} else if cy == deletedY+1 {
			cy--
			cx = cx + deletedX
		}
	} else {
		if cy == deletedY && cx > deletedX {
			cx--
		}
	}
	v.SetCursor(cx, cy)
	// update subsequent charObj'x
	if received.CharValue != 0 {
		for k = range charObjList {
			if deletedY == charObjList[k].EditorPosY && deletedX < charObjList[k].EditorPosX {
				charObjList[k].EditorPosX--
			}
		}
	} else {
		for k = range charObjList {
			if deletedY == charObjList[k].EditorPosY-1 {
				charObjList[k].EditorPosX = deletedX + charObjList[k].EditorPosX
				charObjList[k].EditorPosY--
			} else if deletedY < charObjList[k].EditorPosY-1 {
				charObjList[k].EditorPosY--
			}
		}
	}
}

func compareSlice(a, b []int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

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

// Interface between editor and client
type EditorInterface interface {
	SendCRDTtoEditor(char RPCcharObj, reply *bool) error
	GetDocFromEditor(b bool, data *InitialDoc) error
	UpdateEditorDoc(data InitialDoc, reply *bool) error
	UpdateNeighbours(name string, reply *bool) error
	RemoveNeighbour(name string, reply *bool) error
	GetNeighbours(b bool, names *[]string) error
	Quit(ignore1 int, ignore2 *int) error
}

type EditorRPCServer int

func editorRPC(ipPort string) {
	EditorInterface := new(EditorRPCServer)
	EditorRPC := rpc.NewServer()
	registerServer(EditorRPC, EditorInterface)
	l, e := net.Listen("tcp", ipPort)
	if e != nil {
		log.Fatal("clisten error:", e)
		return
	}

	go EditorRPC.Accept(l)
	runtime.Gosched()
}

func (e *EditorRPCServer) GetNeighbours(b bool, names *[]string) error {
	*names = neighbours
	return nil
}

func printNeighbours(g *gocui.Gui, v *gocui.View) error {
	cx, cy := v.Cursor()
	fmt.Printf("cx=%d, cy=%d, len=%d %v\n", cx, cy, len(neighbours), neighbours)
	return nil
}

func (e *EditorRPCServer) Quit(ignore1 int, ignore2 *int) error {

	gGlobal.Update(updateSideView)
	gGlobal.Close()
	return gocui.ErrQuit

}

func (e *EditorRPCServer) SendCRDTtoEditor(char RPCcharObj, reply *bool) error {
	receivedChar = char
	gGlobal.Update(updateMainView)

	*reply = true
	return nil
}

func updateMainView(g *gocui.Gui) error {
	v, err := g.View("main")
	if err != nil {
		return err
	}
	if receivedChar.Type == "insertion" {
		insertRemote(&receivedChar, v)
	} else {
		deleteRemote(&receivedChar, v)
	}

	return nil
}

// The bool is a dummy variable to satisfy golang RPC interface conventions
func (e *EditorRPCServer) GetDocFromEditor(b bool, data *InitialDoc) error {
	data.Doc = charObjList
	data.CountMap = authorCountMap
	return nil
}

// This function is working; verified by making GetDocFromEditor call
// on client side
func (e *EditorRPCServer) UpdateEditorDoc(data InitialDoc, reply *bool) error {
	charObjList = data.Doc
	authorCountMap = data.CountMap
	gGlobal.Update(displayUpdatedDoc)
	*reply = true
	return nil
}

func (e *EditorRPCServer) UpdateNeighbours(name string, reply *bool) error {
	addNeighbour(name)
	gGlobal.Update(updateSideView)
	*reply = true
	return nil
}

func (e *EditorRPCServer) RemoveNeighbour(name string, reply *bool) error {
	deleteNeighbour(name)
	gGlobal.Update(updateSideView)
	*reply = true
	return nil
}

// Wipes side view and repaints with current neighbours
func updateSideView(g *gocui.Gui) error {
	v, err := g.View("side")
	if err != nil {
		return err
	}
	v.Clear()
	v.Highlight = true
	v.SelBgColor = gocui.ColorGreen
	v.SelFgColor = gocui.ColorBlack
	fmt.Fprintln(v, userName)
	for _, n := range neighbours {
		fmt.Fprintln(v, n)
	}
	return nil
}

// Adds a neighbour to list if it hasn't already been added
func addNeighbour(s string) {
	for _, str := range neighbours {
		if s == str {
			return
		}
	}
	neighbours = append(neighbours, s)
}

func deleteNeighbour(s string) {
	n := len(neighbours)
	// Base cases
	if n == 0 {
		return
	} else if n == 1 && s == neighbours[0] {
		neighbours = make([]string, 0)
		return
	}

	for i, neighbour := range neighbours {
		if s == neighbour {
			neighbours[i] = neighbours[n-1]
			neighbours = neighbours[:n-1]
			return
		}
	}
}

// Wipes side view and repaints with current neighbours
func displayUpdatedDoc(g *gocui.Gui) error {
	v, err := g.View("main")
	if err != nil {
		return err
	}
	v.Clear()
	cx, cy := v.Cursor()
	for _, c := range charObjList {
		v.SetCursor(c.EditorPosX, c.EditorPosY)
		if c.CharValue == 0 {
			v.EditNewLine()
		} else {
			v.EditWrite(c.CharValue)
		}
	}
	v.SetCursor(cx, cy)
	return nil
}

func deleteRPCFromDelBuf(r RPCcharObj) {
	n := len(deletionBuffer)
	// Base cases
	if n == 0 {
		return
	} else if n == 1 && rpcDeepEqual(r, deletionBuffer[0]) {
		deletionBuffer = make([]RPCcharObj, 0)
		return
	}

	for i, rpcObj := range deletionBuffer {
		if rpcDeepEqual(rpcObj, r) {
			deletionBuffer[i] = deletionBuffer[n-1]
			deletionBuffer = deletionBuffer[:n-1]
			return
		}
	}
}

func rpcDeepEqual(r1, r2 RPCcharObj) bool {
	if r1.Author != r2.Author {
		return false
	}
	if r1.CharValue != r2.CharValue {
		return false
	}
	if r1.Opcount != r2.Opcount {
		return false
	}
	if r1.Type != r2.Type {
		return false
	}
	return true
}

func setMapState(author string, count int, s State) {
	ac := AuthorAndCount{Author: author, Count: count}
	authorCountMap[ac] = s
}

func registerServer(server *rpc.Server, s EditorInterface) {
	// registers interface by name of `MyServer`.
	server.RegisterName("EditorListenRPC", s)
}
