package main

import (
	"context"
	"crypto/md5"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
	v1 "github.com/LearningMotors/go-genproto/suki/pb/ssp/v1"
	"github.com/LearningMotors/ms-dev-clients/src/main/asrClient"
	"github.com/google/uuid"
)

const (
	projectID  = "suki-dev"
	bucketName = "ms-dev-clients"
)

type ClientUploader struct {
	cl         *storage.Client
	projectID  string
	bucketName string
	uploadPath string
}

var uploader *ClientUploader

func init() {
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/home/hsahu/.gcp/dev-keyfile.json")
	client, err := storage.NewClient(context.Background())
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	uploader = &ClientUploader{
		cl:         client,
		bucketName: bucketName,
		projectID:  projectID,
		uploadPath: "test-files/",
	}

}

func readFromGCPOuter(w http.ResponseWriter, r *http.Request) {
	readFromGCP()
}

func readFromGCP() (string, error) {
	// Read the object1 from bucket.
	client, err := storage.NewClient(context.Background())
	if err != nil {
		log.Println("Error: ", err)
	}
	rc, err := client.Bucket(bucketName).Object("test-files/audio-sample-1.mp3").NewReader(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		log.Fatal(err)
	}

	id := uuid.New()
	f, err := os.Create(id.String() + ".wav")

	if err != nil {
		log.Fatal(err)
	}

	defer f.Close()

	_, err2 := f.Write(body)

	if err2 != nil {
		log.Fatal(err2)
	}

	fmt.Println("done")
	return id.String() + ".wav", nil
}
func main() {
	http.HandleFunc("/uploadlocal", uploadlocal)
	http.HandleFunc("/uploadgcp", uploadgcp)
	http.HandleFunc("/read", readFromGCPOuter)
	http.HandleFunc("/transcribe", transcribe)
	err := http.ListenAndServe(":9090", nil) // setting listening port
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func transcribe(w http.ResponseWriter, r *http.Request) {
	asrs := []v1.ASR{v1.ASR_GOOGLE}     //, v1.ASR_SUKI}
	audioFilename, err := readFromGCP() // "/home/hsahu/suki/ms-dev-clients/testsSukiAudio.wav"
	if err != nil {
		log.Println("Error: ", err)
		return
	}
	res := asrClient.ProcessWithASRManager(asrs, audioFilename)
	for k, v := range res {
		fmt.Println(k, " : "+v.FinalText+" "+v.NonFinalText)
	}
}

// upload logic
func uploadlocal(w http.ResponseWriter, r *http.Request) {
	fmt.Println("method:", r.Method)
	if r.Method == "GET" {
		crutime := time.Now().Unix()
		h := md5.New()
		io.WriteString(h, strconv.FormatInt(crutime, 10))
		token := fmt.Sprintf("%x", h.Sum(nil))

		t, err := template.ParseFiles("src/main/upload.gtpl")

		if err != nil {
			log.Println("Error:", err)
			return
		}

		t.Execute(w, token)
	} else {
		log.Println("Uploading...")
		r.ParseMultipartForm(32 << 20)
		file, handler, err := r.FormFile("uploadfile")
		if err != nil {
			fmt.Println(err)
			return
		}
		defer file.Close()
		fmt.Fprintf(w, "%v", handler.Header)
		f, err := os.OpenFile("./test/"+handler.Filename, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer f.Close()
		io.Copy(f, file)
	}
}

// upload logic
func uploadgcp(w http.ResponseWriter, r *http.Request) {
	fmt.Println("method:", r.Method)
	if r.Method == "GET" {
		crutime := time.Now().Unix()
		h := md5.New()
		io.WriteString(h, strconv.FormatInt(crutime, 10))
		token := fmt.Sprintf("%x", h.Sum(nil))

		t, err := template.ParseFiles("src/main/upload.gtpl")

		if err != nil {
			log.Println("Error:", err)
			return
		}

		t.Execute(w, token)
	} else {
		log.Println("Uploading...")
		r.ParseMultipartForm(32 << 20)
		file, handler, err := r.FormFile("uploadfile")
		if err != nil {
			fmt.Println(err)
			return
		}
		defer file.Close()
		fmt.Fprintf(w, "%v", handler.Header)
		err = uploader.UploadFile(file, handler.Filename)
		if err != nil {
			log.Println("Error :", err)
		}
	}
}

// UploadFile uploads an object
func (c *ClientUploader) UploadFile(file multipart.File, object string) error {
	ctx := context.Background()

	ctx, cancel := context.WithTimeout(ctx, time.Second*50)
	defer cancel()

	// Upload an object with storage.Writer.
	wc := c.cl.Bucket(c.bucketName).Object(c.uploadPath + object).NewWriter(ctx)
	if _, err := io.Copy(wc, file); err != nil {
		return fmt.Errorf("io.Copy: %v", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("Writer.Close: %v", err)
	}

	return nil
}
