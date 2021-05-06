package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	term "github.com/nsf/termbox-go"
	"github.com/stianeikeland/go-rpio"
	"golang.org/x/net/websocket"
)

var (
	keyPinBCM           = 22
	spkrPinBCM          = 17
	ledPinRedBCM	    = 20
	// ledPinGreenBCM	    = 16
	// ledPinBlueBCM	    = 21
	state               = "idle"
	queue               []string
	outQueue            []string
	bufferReferenceTime int64
	bufferDelay         int64  = 500000 // Default buffer delay
	lastKeyId           string          // identifier for the telegraph that the current queue came from
	lastKeyVal          = "0"
	gpio                bool
	n                   notification
	pingInterval        int64 = 30000 // Interval between test pings to the server (milliseconds)
	pingTimeout         int64 = 5000  // How long to wait after sending a ping before reporting an error (milliseconds)
	pingTimer           int64
	pingOutstanding           = false
	redialInterval      int64 = 1000 // initial number of milliseconds between redial attempts
	lastRedialTime      int64
)

type Config struct {
	Channel string
	Server  string
	Port    string
	Gpio    bool
}

type socketClient struct {
	ip, port, channel, status string
	conn                      *websocket.Conn
}

type morseKey struct {
	lastState, lastDur, lastStart, lastEnd int64
	keyPin                                 rpio.Pin
}

type notification struct {
	state   string
	spkrPin rpio.Pin
	ledPin rpio.Pin
}

func (sc *socketClient) dial(firstDial bool) {

	fmt.Println("Dialing 'ws://" + sc.ip + ":" + sc.port + "/channel/" + sc.channel + "'")

	sc.status = "dialing"
	conn, err := websocket.Dial("ws://"+sc.ip+":"+sc.port+"/channel/"+sc.channel, "", "http://localhost")
	if err == nil {
		sc.conn = conn
		playMorse(".-. . .- -.. -.--")
		sc.status = "connected"
		fmt.Println("sc.status = " + sc.status)
		fmt.Print("sc.conn = ")
		fmt.Println(sc.conn)
		return
	}

	if err != nil {
		fmt.Println("Error connecting to 'ws://" + sc.ip + ":" + sc.port + "/channel/" + sc.channel + "': " + err.Error())
		lastRedialTime = milliseconds()

		redialInterval = redialInterval * 2

		if redialInterval > 30000 {
			redialInterval = 30000
		}

	}

}

func (n *notification) set(value int) {
	if gpio == true {
		if value == 0 {
			n.spkrPin.Write(rpio.Low)
			n.ledPin.Write(rpio.Low)
			n.state = "OFF"

		} else if value == 1 {
			n.spkrPin.Write(rpio.High)
			n.ledPin.Write(rpio.High)
			n.state = "ON"
		} else {
			fmt.Print("Err! Couldn't set notification to: ")
			fmt.Println(value)
		}
	} else {
		if value == 0 {
			// need to figure out the best way to generate + play a tone
			n.state = "OFF"
		} else if value == 1 {
			// need to figure out the best way to generate + play a tone
			n.state = "ON"
		} else {
			fmt.Println("Err! Couldn't set notification")
		}
	}
}

func (n *notification) start() {
	if gpio == true {
		n.spkrPin.Write(rpio.High)
		n.ledPin.Write(rpio.High)
	}
	n.state = "ON"

}

func (n *notification) stop() {
	if gpio == true {
		n.spkrPin.Write(rpio.Low)
		n.ledPin.Write(rpio.Low)
	}
	n.state = "OFF"
}

func playMorse(message string) {
	speed := time.Duration(50)
	for i := 0; i < len(message); i++ {
		switch message[i] {
		case 46: // == "."
			n.start()
			time.Sleep(speed * time.Millisecond)
			n.stop()
			time.Sleep(speed * time.Millisecond)
		case 45: // == "-"
			n.start()
			time.Sleep(3 * speed * time.Millisecond)
			n.stop()
			time.Sleep(speed * time.Millisecond)
		case 32: // == " "
			time.Sleep(3 * speed * time.Millisecond)
		default:
			// Do nothing...
		}
	}
}

func microseconds() int64 {
	t := time.Now().UnixNano()
	us := t / int64(time.Microsecond)
	return us
}

func milliseconds() int64 {
	t := time.Now().UnixNano()
	ms := t / int64(time.Millisecond)
	return ms
}

func (sc *socketClient) onMessage(m string) {

	// Process pongs from the server
	if m == "pong" {
		pingOutstanding = false
		return
	}

	// value := m[:1]            // whether the key went up or down
	ts := m[1 : len(m)-4] // timestamp (in microseconds)
	keyId := m[len(m)-4:] // last 4 digits of message is the key id

	fmt.Print("Received message ")
	fmt.Print(m)
	fmt.Print(" from ")
	fmt.Print(keyId)
	fmt.Print(" at ")
	fmt.Println(time.Now())

	if keyId != lastKeyId { // if its a different telegraph sending
		if len(queue) > 0 {
			// ...and there's already a queue from a different telegraph, do nothing.
			fmt.Print("New telegraph detected, but queue still has messages. Ignoring.")

		} else {
			fmt.Print("New telegraph detected. Setting bufferReferenceTime:")
			lastKeyId = keyId
			tsint64, err := strconv.ParseInt(ts, 10, 64)
			if err != nil {
				fmt.Println(err)
			}
			// Set the time offset between local and remote clients, plus bufferDelay
			bufferReferenceTime = (microseconds() + bufferDelay) - tsint64

			fmt.Println(bufferReferenceTime)

			queue = append(queue, m)
		}

	} else {
		queue = append(queue, m)
	}
}

func (sc *socketClient) listen() {
	fmt.Println("Client listening…")
	var msg string
	for {
		if sc.status == "connected" {
			err := websocket.Message.Receive(sc.conn, &msg)
			if err != nil {
				fmt.Println("Websocket error on Message.Receive(): " + err.Error())
				sc.status = "disconnected"
				playMorse("........")
				sc.dial(false)

			} else {
				sc.onMessage(msg)
			}
		} else {
			time.Sleep(1 * time.Millisecond)
		}
	}
}

func (sc *socketClient) outputListen() {
	for {
		if len(outQueue) > 0 && sc.status == "connected" {

			fmt.Println("Out queue detected in outputListen()")

			fmt.Println("Sending message: " + outQueue[0])
			sendErr := websocket.Message.Send(sc.conn, outQueue[0])
			if sendErr != nil {
				sc.status = "disconnected"
				fmt.Print("sc.conn in send function = ")
				fmt.Println(sc.conn)
				fmt.Println("Could not send message:")
				fmt.Println(sendErr.Error())
				if outQueue[0][:1] != "1" { // Error beep only on keyup, to prevent confusion.
					playMorse("........")
					fmt.Println("Redialling websocket server…")
					fmt.Println("Current status: " + sc.status)
					sc.dial(false)
				}
			} else {
				fmt.Print("Sent: ")
				fmt.Println(outQueue[0])
				outQueue = append(outQueue[:0], outQueue[0+1:]...)
			}
		}
		time.Sleep(1 * time.Millisecond)
	}
}

func main() {
	if len(os.Getenv("TELEGRAPH_CONFIG_PATH")) == 0 {
		os.Setenv("TELEGRAPH_CONFIG_PATH", "config.json")
	}

	file, _ := os.Open(os.Getenv("TELEGRAPH_CONFIG_PATH"))
	decoder := json.NewDecoder(file)
	config := Config{Gpio: true}
	err := decoder.Decode(&config)
	if err != nil {
		fmt.Println("Error reading config.json: ", err)
		fmt.Println("Falling back on default config…")
		config.Channel = "lobby"
		config.Server = "morse.autodidacts.io"
		config.Port = "8000"
	}
	fmt.Println(config.Channel)

	gpio = config.Gpio

	// Initialize morse key
	key := morseKey{lastState: 1, lastDur: 0, lastStart: 0, lastEnd: 0}

	n = notification{state: "OFF"}

	if gpio == true {
		// Setup GPIO
		openErr := rpio.Open()
		if openErr != nil {
			fmt.Println("Error initializing GPIO: " + err.Error())
		}
		keyPn := rpio.Pin(keyPinBCM)
		keyPn.Input()
		spkrPn := rpio.Pin(spkrPinBCM)
		spkrPn.Output()
		ledPn := rpio.Pin(ledPinRedBCM)
		ledPn.Output()
		n.spkrPin = spkrPn
		n.ledPin = ledPn
		key.keyPin = keyPn

		defer rpio.Close()

	} else {
		// Setup for keypress detection
		err := term.Init()
		if err != nil {
			panic(err)
		}

		defer term.Close()
	}

	// Init socketClient & dial websocket
	sc := socketClient{ip: config.Server, port: config.Port, channel: config.Channel}

	sc.dial(true)

	// Start the listener for incoming messages
	go sc.listen()

	// Start the listener that monitors the output queue and sends messages
	go sc.outputListen()

	var gpioKeyVal rpio.State = rpio.High

	// Adding a simplified version of things...
	for {

		if sc.status != "connected" && sc.status != "dialling" {
			fmt.Println("Disconnection detected in main loop. Redialling...")
			playMorse("........")
			sc.dial(false) // Connect if broken
		}

		if sc.status == "dialling" && (milliseconds() > (lastRedialTime + redialInterval)) {
			fmt.Println("Redial timer complete in main loop. Redialling...")
			sc.dial(false)
		}

		var keyVal string

		if gpio == true {
			gpioKeyVal = key.keyPin.Read()
		} else {

		keyPressLoop:
			for {
				switch ev := term.PollEvent(); ev.Type {
				case term.EventKey:
					switch ev.Key {
					case term.KeyEsc:
						os.Exit(1)
					case term.KeySpace:
						gpioKeyVal = rpio.Low
						break keyPressLoop
					case term.KeyEnter:
						gpioKeyVal = rpio.High
						break keyPressLoop
					default:
						break keyPressLoop
					}
				case term.EventError:
					panic(ev.Err)
				}
			}
		}

		if gpioKeyVal == rpio.High {
			keyVal = "0"
		} else {
			keyVal = "1"
		}

		if keyVal != lastKeyVal {
			if sc.status == "connected" {
				fmt.Print("key change: ")
				fmt.Print(lastKeyVal)
				fmt.Print(" → ")
				fmt.Println(keyVal)
				toneVal, _ := strconv.Atoi(keyVal)
				n.set(toneVal)
				timestamp := strconv.FormatInt(microseconds(), 10)
				msg := keyVal + timestamp + "v2"
				outQueue = append(outQueue, msg)
				lastKeyVal = keyVal
			} else {
				playMorse("........")
				redialInterval = 1
			}
		}

		if len(queue) > 0 { // If there's an input queue, parse the next message
			m := queue[0]

			ts := m[1 : len(queue[0])-4]
			ts64, _ := strconv.ParseInt(ts, 10, 64)

			if ts64 < microseconds()-bufferReferenceTime { // If it's time to output this message, do so
				msgValue, _ := strconv.Atoi(m[:1])

				queue = append(queue[:0], queue[0+1:]...) // Pop message out of queue
				n.set(msgValue)

			}
		}

		if sc.status == "connected" {
			// Ping the server periodically to check if we're actually connected
			if milliseconds() > (pingTimer + pingInterval) {
				pingTimer = milliseconds()
				outQueue = append(outQueue, "ping")
				pingOutstanding = true
			}

			if pingOutstanding == true && (milliseconds() > (pingTimer + pingTimeout)) {
				fmt.Println("Server ping timeout. Connection error.")
				sc.status = "disconnected"
				pingOutstanding = false
			}
		}

		time.Sleep(1 * time.Millisecond)
	}
}
