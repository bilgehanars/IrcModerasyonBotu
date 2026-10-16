# Üretim kontrol raporu

Bu rapor uygulamanın test edilen davranışlarını ve gerçek ortamda doğrulanması gereken noktaları ayırır. Testlerin geçmesi canlı IRC/API uyumluluğunun kanıtı değildir.

## İncelemede bulunan ve düzeltilen alanlar

| Alan | Düzenleme |
|---|---|
| API hız/kota hataları | Dakikalık yerel hız sınırı; 429/5xx/ağ hatalarında sınırlı tekrar; Retry-After beklemesi; mesajın toplam yaş sınırına bağlı iptal. |
| Yoğun trafik ve bölünmüş satırlar | Kullanıcı/kanal bazında kısa gruplama; satırları tek metin ve tek puan olarak işleme; OpenAI'de ayrı kullanıcı girdilerini en çok 32'li istekte toplama; Google'da dört sınırlı worker. |
| Geçersiz API yanıtı | Bozuk/aşırı büyük yanıtlar ve aralık dışı skorlar reddedilir. HTTP yönlendirmesi takip edilmez; anahtar başka adrese taşınmaz. |
| Google uyumluluğu | Native kategori adları; REST v2; API anahtarı HTTP başlığında; desteklenmeyen dil için açık izin gerekir. |
| IRC başlangıcı | Kayıt, OPER ve JOIN için 45 saniye zaman aşımı; kanal giriş reddinde oturum kapanır; account-tag olmadan moderasyon başlatılmaz. |
| IRC gönderimleri | Sınırlı gönderim kuyruğu, güvenli satır uzunluğu ve UTF-8 NOTICE kısaltması. Uzun admin cevapları okuma döngüsünde ağ hız sınırını beklemez. |
| Master kimliği | Sunucu tarafından doğrulanmış account-tag; nick benzerliği yetki vermez; hesap adı sembolleri RFC1459 nick eşlemesiyle genişletilmez. |
| Kalıcı ayarlar | SQLite'a yazılmadan değişiklik uygulanmaz. Düzenlenebilir alanlar izin listesiyle sınırlı; anahtarlar IRC komutlarında gösterilmez/değiştirilmez. |
| Af ve muafiyet yarışı | Politika revizyonu ve eylem kilidi: önceden bekleyen API sonuçları değişiklikten sonra uygulanmaz. |
| Ban geçmişi | Gönderimden önce pending kayıt; geçerli üst basamaklı aktif talep varken daha düşük ban yeniden gönderilmez. Af yalnızca kayıtlı bot taleplerini hedefler. |
| Deneme modu | Puan kaydı devam eder; gerçek ban/cooldown kaydı oluşturmaz. |
| SQLite | Transaction, indeksler, foreign key, her bağlantıda busy timeout/foreign key; şema sürümü; süreç kilidi ile aynı veritabanında ikinci bot engellenir. |
| Conf üretimi | -init; terminalde gizli şifre girişi; 0600 dosya; mevcut dosyayı ezmez; başarısız kurulumda yarım dosya bırakmaz. |
| Ayar doğrulama | NaN/Inf eşikleri, taşabilecek süreler, aşırı liste/kuyruk boyutları ve kontrol karakterleri reddedilir. |

## Otomatik doğrulama

```sh
go test -race -timeout 90s ./...
go vet ./...
CGO_ENABLED=0 go build -o ircmoderasyonbotu .
./ircmoderasyonbotu -config bilgehan.conf.example -check
./ircmoderasyonbotu -config bilgehan.google.conf.example -check
```

Testler gerçek API anahtarı kullanmaz. Yerel HTTP sunucuları ve TCP tabanlı IRC taklidi ile provider istekleri, OPER/JOIN/PING, hesap doğrulaması, ceza, af, exception ve kalıcılık kontrol edilir. Otuz bağımsız kullanıcının tek OpenAI isteğindeki eşleşmesi ile dörde bölünmüş cümlenin tek puanlanması test edilir. Sorgular SQL parametreleri kullanır; master için serbest SQL/RAW komut arayüzü yoktur.

Bu değişiklikte Go 1.26.9 ile `govulncheck ./...` çalıştırıldı ve bilinen, erişilebilir güvenlik açığı raporlanmadı (`No vulnerabilities found`). Araç zinciri veya bağımlılıklar değiştikçe tarama tekrarlanmalıdır.

## Canlı ortam sınırları

- Botun KLINE/ZLINE ve kaldırma komutları UnrealIRCd biçimindedir. Başka IRCd'ler uyumlu sayılmaz.
- Bir IRC komutunun kuyruğa alınması sunucunun banı uyguladığını kanıtlamaz. Sunucu ban listesi/oper logu manuel doğrulanmalıdır. `pending` veya `removal_requested` kaydı belirsiz bir teslimi temsil edebilir.
- KLINE/ZLINE yerel sunucuda etkilidir. Çok sunuculu ağ için global yaptırım uygulanmaz.
- Kimlik host/IP temellidir. Host değişikliği yeni kayıt, ortak IP ise ortak yaptırım anlamına gelir. Nick/hesap muafiyeti mevcut bir IP banını aşamaz.
- Bot yalnızca katıldığı kanalları görür. Mesaj kuyruğu dolarsa veya mesaj eskirse kontrol atlanır; tüm mesajların inceleneceği garanti edilmez.
- Birikmekte olan mesajlar ve API kuyruğu bellektedir; kapanma/yeniden bağlanmada henüz işlenmemiş gruplar kaybolur. SQLite yalnızca verilmiş puanları, ayarları ve ceza geçmişini saklar.
- Yerel istek sınırı provider token/günlük kotasını ölçmez. API arızasında mesaja puan veya ban verilmez.
- Google Türkçe kalitesi doğrulanmış destek kapsamında değildir. Native kategoriler OpenAI kategorileriyle eşdeğer değildir. Gerçek hesap/API çağrısı bu ortamda yapılmamıştır.
- `dry_run: false` ayarını etkinleştirmeden önce kendi kanalındaki yanlış pozitifler ve sunucu yetkileri gözlenmelidir.
