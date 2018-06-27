# GOllaborate
Collaborative document editor with terminal-based UI

IMPORTANT: This academic project was completed as part of course requirements for UBC CPSC416. It is uploaded for
			     code sample purposes. 

1: STRUCTURE
The Gollaborate directory structure is depicted below:

proj2_c1l0b_c9g0b_q8h0b_v9i0b/
	- Client
			- client2.go
	- server
			- server2.go
	- editor
			- myeditor.go
	- proposal
			- proposal.pdf
	- report
			- report.pdf
	- README.txt

2: GOCUI
IMPORTANT: The Gollaborate application uses a third-party library "gocui" for its terminal GUI. Please install this
package on the target machine before attempting to run the application by executing the following command:
		<go get github.com/jroimartin/gocui>

3: HOW TO RUN
To run one instance of the Gollaborate text editor, 3 files are of interest:
	(1) server2.go: This file accepts 1 command-line arguments: (1) The IP and port to listen for incoming client connection
		 							requests
	(2) client2.go: This file accepts 3 command-line arguments: (1) IP:PORT that server is listening on, (2) the IP:PORT to 									listen to for incoming connection requests from other client nodes and (3) the IP:PORT to listen to for 									incoming connection request from editor
	(3) myeditor.go: This file accepts 4 command-line arguments: (1) User name (2) the IP:PORT that the client is listening on 										 for connection request from editor (3) IP:PORT that editor is listening for connection request from client 
									 and (4) the document ID. 

The server should be instantiated first, but only one instance needs to be running. For each user that is editing a particular document, a client and application node has to be instantiated in that order. Thus, if one user wishes to edit 2 documents, it is necessary to instantiate two distinct client-app node pairs. These pairs of nodes should have different sets of ports. 

============================================
3.1: HOW TO RUN LOCALLY
If testing on local computer with 3 users and 1 document:

#1 - Open 1 terminal for the server and run the command: <go run server2.go 127.0.0.1:5555>
#2 - Open 2 terminals for user 1. In the first of two terminals, input the following command: 
		 		  <go run client2.go 127.0.0.1:5555 127.0.0.1:9999 127.0.0.1:4322>
     In the second of two terminals, input the following command: 
					<go run myeditor.go John 127.0.0.1:4322 127.0.0.1:8888 doc1>
#3 - Repeat step 2 with the commands below:

#3.1: User 2 client: <go run client2.go 127.0.0.1:5555 127.0.0.1:7777 127.0.0.1:4200>
		  User 2 application: <go run myeditor.go Jane 127.0.0.1:4200 127.0.0.1:7776 doc1>

#3.2: User 3 client: <go run client2.go 127.0.0.1:5555 127.0.0.1:7000 127.0.0.1:4100>
		  User 3 application: <go run myeditor.go Tifa 127.0.0.1:4100 127.0.0.1:7001 doc1>


3.2: HOW TO RUN REMOTELY
If testing on Azure:

#1 - Allocate 1 virtual machine instance for a server, and 1 VM instance per client-application pair. 
#2 - On the server VM, switch to the server directory and run the following command:
		 go run server2.go <server public IP>:5555
#3 - For each client, run the following two commands on different terminals of the same VM instance:
#3.1:
	 - Switch to the Client directory and run:
		 go run client2.go <server public IP>:5555 <VM public IP>:9999 127.0.0.1:4322
#3.2:
	 - Switch to the editor directory and run:
		 go run myeditor.go MyNameGoesHere 127.0.0.1:4322 127.0.0.1:8888 DocIDGoesHere
#4 - Repeat step 3 - 3.2 as many times as necessary for each user

N.B.: It is not necessary to run every unique client-application pair on a distinct VM instance. 



4: SYSTEM LIMITATIONS

#1 - If you are having compatibility issue running gocui on your terminal, i.e. editor is not showing up or there's some wired character, consider using MobaXterm on Windows, or iTerm on Mac.
#2 - We want to limit the complexity of editor so we just implemented some basic functions: insert one char, delete one char.
    Here are some functions we didn't implement:
    - warp around
    - copy/paste
    - hold down a key to repeat insertion/deletion
    - redo/undo
    - save/load file 




