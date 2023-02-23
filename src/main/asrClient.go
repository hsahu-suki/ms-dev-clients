package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/LearningMotors/go-genproto/suki/pb/ssp/asr_manager"
	v1 "github.com/LearningMotors/go-genproto/suki/pb/ssp/v1"
	"github.com/LearningMotors/platform/redis"
	ssputils "github.com/LearningMotors/platform/sspv2"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

//use following fommand to listen from command line:
//go build -o livecaption source/zextras/audioProducer.go ; gst-launch-1.0 -v pulsesrc ! audioconvert ! audioresample ! audio/x-raw,channels=1,rate=16000 ! filesink location=/dev/stdout | ./livecaption

type fileContent struct {
	fname  string
	offSet int64
	fPtr   *os.File
}

var fileDetails map[string]*fileContent
var content = make([]byte, 3200)
var ASR_USED = v1.ASR_GOOGLE

func getAudioContent(filename string) ([]byte, error) {
	c, ok := fileDetails[filename]

	if !ok {
		fmt.Println("Opening Audio File ")
		f, err := os.OpenFile(filename, os.O_RDWR, 0755)
		if err != nil {
			fmt.Println("Error while opening the file: ", err)
			return nil, err
		}

		c = &fileContent{
			fname:  filename,
			offSet: 0,
			fPtr:   f,
		}

		fileDetails[filename] = c
	}

	newOffset, err := c.fPtr.ReadAt(content, c.offSet)

	if err != nil {
		if err == io.EOF {
			c.fPtr.Close()
			delete(fileDetails, filename)
		}
		return nil, err
	}
	c.offSet = c.offSet + int64(newOffset)
	return content, nil
}

var rClient = redis.NewRedisClient("localhost:6379", 0)

//var rClient = redis.NewRedisClient("ms-stream.microservices:6379", 0)

// var rClient = redis.NewRedisClient("host.docker.internal:6379", 0)
var buf = make([]byte, 1024)

func getAudioFromCmdMicroPhone() ([]byte, error) {

	n, err := os.Stdin.Read(buf)
	//log.Println("========n=", n)
	if err != nil {
		log.Fatalf("failed while os.Stdin.Read(buf): %v", err)
		return nil, err
	}
	if n > 0 {
		return buf, nil
	}
	return buf, nil
}

func putAudioInTheStream(fname string, sessionID string) {

	if fileDetails == nil {
		fileDetails = make(map[string]*fileContent)
	}
	streamName := ssputils.GenerateAudioStreamName(sessionID)

	log.Println("Writing to: ", streamName)
	if rClient == nil {
		log.Print("rClient is not up.")
	}
	for {
		data, err := getAudioContent(fname)
		//log.Println("Read: ", data)
		if err != nil {
			log.Println("Exiting: Done Sending audio.", err)
			err = rClient.AddEntryToStream(context.Background(), streamName, redis.StreamKeySignal, redis.StreamValueAudioEnd.String())
			return
		}
		err = rClient.AddEntryToStream(context.Background(), streamName, redis.StreamKeyAudio, data)
		if err != nil {
			log.Println(err)
			return
		}
	}
}

func getTranscriptFromStream(sessionID string, asr v1.ASR) {
	defer wg1.Done()
	var transcriptRes v1.TranscriptResult
	id := "0"
	//streamName := ssputils.GenerateAudioStreamName(sessionID)
	streamName := ssputils.GenerateTranscriptStreamName(sessionID, strings.ToLower(asr.String()))
	log.Println("Reading from: ", streamName)
	for {
		nextID, res, err := rClient.ReadLatestFromStreamTimeout(context.Background(), streamName, id, 3*time.Second)
		if err != nil {
			log.Println(err)
			return
		}
		id = nextID
		//log.Print("Read: ", res)
		for k, v := range res {
			//log.Println("Key: ", k)
			if k == redis.StreamKeyTranscript.String() {
				e := proto.Unmarshal([]byte(v.(string)), &transcriptRes)
				if e != nil {
					log.Println("======", e)
				}
				//log.Println(asr, ":", transcriptRes.Transcript.TranscriptOrIntent)
				process(asr, transcriptRes.Transcript.TranscriptOrIntent, transcriptRes.IsFinal)
			} else if k == redis.StreamKeySignal.String() {
				if v == redis.StreamValueEndTranscript.String() {
					log.Println("========= ", asr, " TranscriptionEnded ============")
					return
				} else {
					log.Println("Unhandled Signal Value")
					return
				}
			}
		}
	}
}

var (
	tls        = flag.Bool("tls", false, "Connection uses TLS if true, else plain TCP")
	caFile     = flag.String("ca_file", "", "The file containing the CA root cert file")
	serverAddr = flag.String("addr", "localhost:10002", "The server address in the format of host:port")
	//serverAddr = flag.String("addr", "ms-asr-manager.microservices:10001", "The server address in the format of host:port")

	//serverAddr         = flag.String("addr", "host.docker.internal:10001", "The server address in the format of host:port")
	serverHostOverride = flag.String("server_host_override", "x.test.example.com", "The server name used to verify the hostname returned by the TLS handshake")
)

var clear map[string]func() //create a map for storing clear funcs

func init() {
	clear = make(map[string]func()) //Initialize it
	clear["linux"] = func() {
		cmd := exec.Command("clear") //Linux example, its tested
		cmd.Stdout = os.Stdout
		cmd.Run()
	}
	clear["windows"] = func() {
		cmd := exec.Command("cmd", "/c", "cls") //Windows example, its tested
		cmd.Stdout = os.Stdout
		cmd.Run()
	}
}

func CallClear() {
	value, ok := clear[runtime.GOOS] //runtime.GOOS -> linux, windows, darwin etc.
	if ok {                          //if we defined a clear func for that platform:
		value() //we execute it
	} else { //unsupported platform
		panic("Your platform is unsupported! I can't clear terminal screen :(")
	}
}

func connectToASRManager(asrs []v1.ASR, sessionID string, filename string) {

	flag.Parse()
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))

	conn, err := grpc.Dial(*serverAddr, opts...)
	if err != nil {
		log.Fatalf("fail to dial: %v", err)
	}
	defer conn.Close()

	ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
	client := asr_manager.NewASRManagerClient(conn)
	x, err := client.StartSpeech(ctx, &asr_manager.StartSpeechRequest{
		SessionId: sessionID,
		SpeechConfig: &v1.SpeechConfig{
			Asrs: asrs,
			AudioConfiguration: &v1.AudioConfig{
				AudioEncoding:   v1.AudioEncoding_LINEAR16,
				SampleRateHertz: 16000,
				AudioLanguage:   "en-us",
			},
		},
	})

	if err != nil {
		log.Fatal("Error While connecting to asr manager: ", err)
	}
	log.Println(x)
	go putAudioInTheStream(filename, sessionID)
	for _, asr := range asrs {
		log.Println("Starting WG for ", asr)
		go getTranscriptFromStream(sessionID, v1.ASR(asr))
	}
}

var asrTransMap sync.Map

type Trans struct {
	FinalText    string
	NonFinalText string
}

var sukiTranscript Trans
var googleTranscript Trans

func process(asr v1.ASR, text string, isFinal bool) {
	x, ok := m[asr]

	if ok {
		if isFinal {
			x.FinalText = x.FinalText + " " + text
			x.NonFinalText = ""

		} else {
			x.NonFinalText = text
		}
	} else {
		log.Println("================Error==================")
	}

	//log.Println("========" + x.FinalText + "======" + x.NonFinalText)
}

func display() {
	for {
		time.Sleep(time.Second * 1)
		//CallClear()
		for k, v := range m {
			log.Println(k, " : "+v.FinalText+" "+v.NonFinalText)
		}
	}
}

var m map[v1.ASR]*Trans

func cleanRedisSteams(sessionId string, asrs []v1.ASR) {
	arr := []string{sessionId + "-audio"}

	for _, asr := range asrs {
		arr = append(arr, ssputils.GenerateTranscriptStreamName(sessionId, strings.ToLower(asr.String())))
	}

	rClient.RemoveKeys(context.Background(), arr)
}

var wg1 sync.WaitGroup

func main1() {
	asrs := []v1.ASR{v1.ASR_GOOGLE} //, v1.ASR_SUKI}
	audioFilename := "/home/hsahu/suki/ms-dev-clients/testsSukiAudio.wav"
	res := ProcessWithASRManager(asrs, audioFilename)
	for k, v := range res {
		log.Println(k, " : "+v.FinalText+" "+v.NonFinalText)
	}
}

func ProcessWithASRManager(asrs []v1.ASR, audioFile string) map[v1.ASR]*Trans {

	id := uuid.New()
	m = make(map[v1.ASR]*Trans)

	m[v1.ASR_GOOGLE] = &Trans{}
	m[v1.ASR_SUKI] = &Trans{}
	//go display()
	log.Println("Weight Group is on following len: ", len(asrs))
	wg1.Add(1)
	sessionID := id.String()
	defer cleanRedisSteams(sessionID, asrs)

	fmt.Print("SessionID: ", sessionID, "\n")
	connectToASRManager(asrs, sessionID, audioFile) //"/home/hsahu/suki/ms-dev-clients/testsSukiAudio.wav")
	log.Println("Waiting for transcription to finish.")
	log.Printf("WG Status: %v", wg1)
	wg1.Wait()
	log.Println("Done waiting for all ASRs.")
	return m
}
