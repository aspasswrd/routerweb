package main

import (
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Device struct {
	Name string `json:"name"`
	MAC  string `json:"mac"`
	Mode string `json:"mode"`
}

var modes = map[string]string{
	"direct":    "Напрямую",
	"selective": "Выборочно",
	"foreign":   "Всё кроме РФ",
	"force":     "Всё через VPN",
}

func main() {
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = ":8080"
	}
	dataFile := os.Getenv("DATA_FILE")
	if dataFile == "" {
		dataFile = "data/devices.json"
	}
	if err := os.MkdirAll(filepath.Dir(dataFile), 0755); err != nil {
		log.Fatal(err)
	}

	devices := []Device{}
	data, err := os.ReadFile(dataFile)
	if err == nil {
		if err := json.Unmarshal(data, &devices); err != nil {
			log.Fatal(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Fatal(err)
	}

	page := template.Must(template.ParseFiles("static/index.html"))
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := page.Execute(w, struct {
			Devices []Device
			Modes   map[string]string
		}{devices, modes}); err != nil {
			log.Print(err)
		}
	})
	mux.HandleFunc("POST /devices", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Некорректная форма", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		mac, err := net.ParseMAC(r.FormValue("mac"))
		mode := r.FormValue("mode")
		if err != nil || len(mac) != 6 || name == "" || len([]rune(name)) > 40 || modes[mode] == "" {
			http.Error(w, "Проверьте имя, MAC-адрес и режим", http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		next := append([]Device{}, devices...)
		device := Device{Name: name, MAC: mac.String(), Mode: mode}
		found := false
		for i := range next {
			if next[i].MAC == device.MAC {
				next[i] = device
				found = true
				break
			}
		}
		if !found {
			next = append(next, device)
		}
		data, err := json.MarshalIndent(next, "", "  ")
		if err == nil {
			err = os.WriteFile(dataFile+".tmp", data, 0644)
		}
		if err == nil {
			err = os.Rename(dataFile+".tmp", dataFile)
		}
		if err != nil {
			log.Print(err)
			http.Error(w, "Не удалось сохранить устройство", http.StatusInternalServerError)
			return
		}
		devices = next
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("routerweb listening on %s", address)
	log.Fatal(server.ListenAndServe())
}
