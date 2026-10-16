package main

import (
	"context"
	"flag"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	path := flag.String("config", "bilgehan.conf", "JSON yapılandırma dosyası")
	initConfig := flag.Bool("init", false, "Etkileşimli bilgehan.conf oluştur")
	check := flag.Bool("check", false, "Yapılandırmayı doğrula ve çık")
	flag.Parse()
	if *initConfig {
		if err := generateConfig(os.Stdin, os.Stdout, *path); err != nil {
			log.Fatal(err)
		}
		return
	}
	c, err := loadConfig(*path)
	if err != nil {
		log.Fatalf("Yapılandırma yüklenemedi: %v", err)
	}
	if *check {
		log.Print("Yapılandırma geçerli")
		return
	}
	store, err := openStore(c.Moderation.StateFile)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	if _, err = newRuntime(c, store); err != nil {
		log.Fatalf("Kalıcı ayar hatası: %v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	mod := newModerator(c)
	min := time.Duration(c.IRC.ReconnectMinSeconds) * time.Second
	max := time.Duration(c.IRC.ReconnectMaxSeconds) * time.Second
	delay := min
	for ctx.Err() == nil {
		started := time.Now()
		err = runSession(ctx, c, store, mod)
		if ctx.Err() != nil {
			break
		}
		log.Printf("Oturum sona erdi: %v", err)
		if time.Since(started) > 5*time.Minute {
			delay = min
		}
		wait := delay + time.Duration(rand.Int63n(int64(delay/4)+1))
		log.Printf("%s sonra yeniden bağlanılacak", wait.Round(time.Second))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay *= 2
		if delay > max {
			delay = max
		}
	}
}
