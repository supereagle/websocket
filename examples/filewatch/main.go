// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"flag"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write the file to the client.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the client.
	pongWait = 60 * time.Second

	// Send pings to client with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Poll file for changes with this period.
	filePeriod = 5 * time.Second
)

var (
	addr      = flag.String("addr", ":8080", "http service address")
	homeTempl = template.Must(template.New("").Parse(homeHTML))
	filename  []string
	upgrader  = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	rd *bufio.Reader 
)

func readFileIfModified(lastMod time.Time) ([]byte, time.Time, error) {
	fi, err := os.Stat(filename[0])
	if err != nil {
		return nil, lastMod, err
	}
	if !fi.ModTime().After(lastMod) {
		return nil, lastMod, nil
	}
	p, err := ioutil.ReadFile(filename[0])
	if err != nil {
		return nil, fi.ModTime(), err
	}
	return p, fi.ModTime(), nil
}

func tailFile(filename []string, data chan []byte, stop chan int) {
	f1, err := os.Open(filename[0])
	if err != nil {
		data <- []byte("Interval error happens, TERMINATE")
		stop <- 1
		return
	}
	defer f1.Close()
	rd = bufio.NewReader(f1)

	if len(filename) > 1 {
		f2, err := os.Open(filename[1])
		if err == nil {
			go changeTailFile(f2)
		}
		defer f2.Close()
	}
	
	for {
		line, err := rd.ReadBytes('\n')

		if io.EOF == err {
			continue
		}

		if err != nil  {
			data <- []byte("Interval error happens, TERMINATE")
			stop <- 1
			break
		}
		data <- line
	}
}

func changeTailFile(file *os.File) {
	time.Sleep( 20 * time.Second)
	rd = bufio.NewReader(file)
}

func reader(ws *websocket.Conn) {
	defer ws.Close()
	ws.SetReadLimit(512)
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			break
		}
	}
}

func appendFile() {
	var file2 *os.File

	file1, err := os.OpenFile(filename[0], os.O_APPEND | os.O_RDWR, 0666)
	defer file1.Close()

	if err != nil {
		log.Fatal(err)
	}

	if len(filename) > 1 {
		file2, err = os.OpenFile(filename[1], os.O_APPEND | os.O_RDWR, 0666)
		defer file2.Close()
	
		if err != nil {
			log.Fatal(err)
		}
	}

	fileTicker := time.NewTicker(filePeriod)
	for {
		select {
		case <-fileTicker.C:
			_, err = file1.WriteString("hello a\n")
			if err != nil {
				log.Fatal(err)
			}

			if len(filename) > 1 {
				_, err = file2.WriteString("hello b\n")
				if err != nil {
					log.Fatal(err)
				}
			}
		}
	}
}

func writer(ws *websocket.Conn, lastMod time.Time) {
	data := make(chan []byte, 10)
	var p []byte
	stop := make(chan int, 0)

	pingTicker := time.NewTicker(pingPeriod)
	defer func() {
		pingTicker.Stop()
		ws.Close()
	}()

	go tailFile(filename, data, stop)
	for {
		select {
		case p = <-data:
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := ws.WriteMessage(websocket.TextMessage, p); err != nil {
				return
			}
		case <-stop:
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			ws.WriteMessage(websocket.CloseMessage, []byte{})
			return
		case <-pingTicker.C:
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := ws.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

func serveWs(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		if _, ok := err.(websocket.HandshakeError); !ok {
			log.Println(err)
		}
		return
	}

	var lastMod time.Time
	if n, err := strconv.ParseInt(r.FormValue("lastMod"), 16, 64); err == nil {
		lastMod = time.Unix(0, n)
	}

	go writer(ws, lastMod)
	go appendFile()
	reader(ws)
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "Not found", 404)
		return
	}
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	p, lastMod, err := readFileIfModified(time.Time{})
	if err != nil {
		p = []byte(err.Error())
		lastMod = time.Unix(0, 0)
	}
	var v = struct {
		Host    string
		Data    string
		LastMod string
	}{
		r.Host,
		string(p),
		strconv.FormatInt(lastMod.UnixNano(), 16),
	}
	homeTempl.Execute(w, &v)
}

func main() {
	flag.Parse()
	if flag.NArg() < 1 {
		log.Fatal("filename not specified")
	}
	filename = flag.Args()[0:]
	http.HandleFunc("/", serveHome)
	http.HandleFunc("/ws", serveWs)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		log.Fatal(err)
	}
}

const homeHTML = `<!DOCTYPE html>
<html lang="en">
    <head>
        <title>WebSocket Example</title>
    </head>
    <body>
        <pre id="fileData">{{.Data}}</pre>
        <script type="text/javascript">
            (function() {
                var data = document.getElementById("fileData");
                var conn = new WebSocket("ws://{{.Host}}/ws?lastMod={{.LastMod}}");
                conn.onclose = function(evt) {
                    data.textContent = 'Connection closed';
                }
                conn.onmessage = function(evt) {
                    console.log('file updated');
                    data.textContent = evt.data;
                }
            })();
        </script>
    </body>
</html>
`
