# IrcModerasyonBotu

Go ile yazılmış IRC moderasyon botu. OpenAI Moderation API kullanır; harici Go paketi gerektirmez. IRC sunucusuna normal istemci olarak bağlanır, OPER girişi yapar, yapılandırılmış kanallardaki mesajları denetler. Özel mesajlar yalnızca sahip komutları için kullanılır.

## Kurulum

Go 1.23 veya üzeri gerekir. Linux sunucuda:

```sh
git clone https://github.com/bilgehanars/IrcModerasyonBotu.git
cd IrcModerasyonBotu
cp bilgehan.conf.example bilgehan.conf
chmod 600 bilgehan.conf
# bilgehan.conf içindeki bağlantı bilgilerini, API anahtarını ve sahip hesabını düzenle
go build -o ircmoderasyonbotu .
./ircmoderasyonbotu -config bilgehan.conf -check
./ircmoderasyonbotu -config bilgehan.conf
```

`bilgehan.conf` JSON biçimindedir; yorum satırı kabul etmez. Tüm bağlantı bilgileri ve kurallar bu dosyadan okunur. Dosya değiştiğinde botu yeniden başlat. Gerçek yapılandırma ve puan dosyası Git tarafından yok sayılır.

Örnek ayarda `dry_run: true` olduğu için OPER girişi ve API kontrolleri çalışır, ceza komutları yalnızca loga yazılır. Otomatik yaptırımları etkinleştirmek için `dry_run` değerini `false` yap. Deneme modunda da puan ve ceza simülasyon geçmişi tutulur; canlı kullanıma geçerken gerektiğinde `reset` komutuyla temizle.

## Puan ve ceza sistemi

`rules` altındaki eşik, modelin kategoriye ilişkin güven skorudur; kullanıcının suçluluk olasılığı veya cezanın ağırlığı değildir. `points` ise botun kullanıcıya ekleyeceği puandır. Türkçe kanal mesajlarıyla deneme yaparak eşikleri kendin ayarla; alıntılar, şakalar ve şiddet haberleri de kategoriye girebilir.

- Bir mesaj için eşik aşan kategorilerden **en yüksek puan** eklenir, kategoriler toplanmaz.
- Puanlar nick yerine sunucunun mesaj önekinde gönderdiği **tam host/IP** üzerinden birikir. Nick değiştirmek puanı sıfırlamaz. Host değişirse yeni kayıt oluşur; ortak host/IP kullananlar aynı kaydı paylaşır.
- `score_window_hours` kayan puan penceresidir. Örnekte son 24 saat geçerlidir. Süresi dolan puanlar hesaplamada dışarıda kalır.
- Toplam puana uyan en yüksek ceza seçilir. Örnekte 10 puan → 30 dakika, 20 puan → 24 saat, 35 puan → süresiz KLINE.
- `duration: "0"` süresiz demektir; `30m`, `24h`, `168h` süreli cezalardır. Gün için `24h` kullan; `1d` Go süre biçimi değildir.
- Aynı veya daha düşük ceza basamağı cooldown dolmadan tekrar gönderilmez. Daha yüksek basamak beklemeden uygulanabilir. Cooldown ban süresinden bağımsızdır.
- Puanlar ve son ceza talebi `state_file` dosyasına atomik olarak yazılır; bot yeniden başladığında yüklenir. Bozuk dosyada bot durur, puanları sessizce sıfırlamaz.
- API hatası, zaman aşımı, dolu kuyruk veya çok eski mesaj için puan/ceza verilmez. Kuyruk dolması loglanır. API'ye her seferinde tek mesaj gönderilir, sohbet geçmişi gönderilmez.

## IRC sunucusu ve yetkiler

Komut biçimi **UnrealIRCd** içindir:

```text
KLINE *@tam-host 1800 :neden
ZLINE *@203.0.113.7 1800 :neden
KLINE *@tam-host 0 :neden
```

Ceza ayarındaki `command` değeri `KLINE` veya `ZLINE` olabilir. ZLINE yalnızca sunucunun mesaj önekinde gerçek IP gönderdiği durumda kullanılır; cloak/hostname DNS ile çözülmez, nick üzerinden tahmin yapılmaz. IP görünmüyorsa puan kaydedilir, ZLINE atlanır ve log yazılır. KLINE görünen tam hostu hedefler; cloakların ban eşleşmesi IRCd yapılandırmasına bağlıdır. Canlı kullanımdan önce kendi sunucunda doğrula.

KLINE ve ZLINE **yerel sunucu** yaptırımlarıdır; bağlı IRC ağının tamamını otomatik olarak kapsamaz. Bot yalnızca katıldığı kanalları görür. Ergo dahil başka IRCd'lerde komut desteği ve sözdizimi farklı olabilir; bu sürüm UnrealIRCd dışındaki sunucular için uyumluluk iddia etmez. Sunucu servis linki veya tüm ağ mesajlarını dinleyen bir modül değildir.

Botun OPER hesabına gerekli KLINE/ZLINE yetkilerini sunucuda tanımla. Bot `381` OPER başarı yanıtını almadan kanallara girmez. Başarısız OPER girişinde bağlantıyı yeniler. KLINE/ZLINE gönderimi logda **talep** olarak kaydedilir; sunucunun komutu uyguladığını doğrulayan bir ban listesi sorgusu yapılmaz. Sunucunun oper loglarını kontrol et.

TLS varsayılan olarak açık, sertifika doğrulaması zorunludur. Bağlantı kesilirse artan bekleme ve küçük rastgele gecikme ile yeniden bağlanır; OPER ve JOIN tekrar yapılır. PING/PONG ve okuma zaman aşımı takılı bağlantıları kapatır. Önceki bağlantının kuyruğu yeni bağlantıda kullanılmaz. Kanaldan KICK edilirse yeniden bağlanır.

## Sahip kontrolü

`owner_accounts` bir **NickServ/IRC hesap adı** listesidir; nick veya hostmask listesi değildir. Sahip sunucuda hesabına giriş yapmış olmalıdır. Sunucunun IRCv3 `account-tag` desteği zorunludur. Bot bu yeteneği görüşür ve yalnızca sunucunun gönderdiği `account` etiketi listedeki hesapla eşleşirse yönetim komutlarını kabul eder. Yalnızca aynı nicki almak yetki vermez. `account-tag` yoksa sahip komutları devre dışıdır; moderasyon devam eder.

Sahibin doğrulanmış hesaptan yazdığı kanal mesajları moderasyondan muaftır.

```text
/msg BilgehanBot help
/msg BilgehanBot status
/msg BilgehanBot score kullanicinin.tam.hostu
/msg BilgehanBot score 203.0.113.7
/msg BilgehanBot reset kullanicinin.tam.hostu
```

`reset` yalnızca botun puan/ceza geçmişini temizler. IRC sunucusundaki mevcut banı kaldırmaz. Host/IP logdaki `host=` alanından alınabilir. Genel RAW/OPER komut çalıştırma arayüzü yoktur.

## Servis olarak çalıştırma

Depoyu `/opt/IrcModerasyonBotu` içine yerleştirip derledikten sonra örnek `deploy/ircmoderasyonbotu.service` dosyasını kullanabilirsin. Önce sistem kullanıcısını oluştur ve dizin sahipliğini ayarla:

```sh
sudo useradd --system --home /opt/IrcModerasyonBotu --shell /usr/sbin/nologin ircmod
sudo chown -R ircmod:ircmod /opt/IrcModerasyonBotu
sudo chmod 600 /opt/IrcModerasyonBotu/bilgehan.conf
sudo cp deploy/ircmoderasyonbotu.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ircmoderasyonbotu
sudo journalctl -u ircmoderasyonbotu -f
```

Mesaj içerikleri loga veya puan dosyasına yazılmaz. Denetlenen kanal mesajlarının metni OpenAI API'ye iletilir. Host/IP, kategori ve puanlar logda; host/IP, puan zamanları ve son ceza bilgisi veri dosyasında bulunur. API anahtarı ve OPER şifresi loglanmaz.

## Doğrulama

```sh
go test -race ./...
go vet ./...
```

Testler gerçek bir IRC ağına veya OpenAI hesabına bağlanmaz; yerel TCP/HTTP sunucularıyla kayıt, CAP, OPER, PING/PONG, API, dry-run ve ceza gönderimini kontrol eder.

Kaynaklar: [OpenAI Moderation](https://developers.openai.com/api/docs/guides/moderation), [UnrealIRCd komutları](https://www.unrealircd.org/docs/User_%26_Oper_commands).
