# IrcModerasyonBotu

IRC kanallarındaki mesajları denetleyen, tekrar eden ihlallere puan veren ve belirlenen puana ulaşıldığında kullanıcıya sunucu yasağı uygulayan Go botu.

Bot kanala bir IRC kullanıcısı gibi bağlanır. Mesajları seçilen OpenAI veya Google Cloud moderasyon API'sine gönderir; taciz, nefret söylemi, tehdit ve şiddet kategorileri için gelen sonuçları yapılandırdığın kurallarla değerlendirir. Puanlar SQLite veritabanında saklanır. Bağlantı kesilirse yeniden bağlanır, OPER girişi yapar ve kanallara tekrar katılır.

**Bu rehber Ubuntu/Debian tabanlı Linux sunucular için hazırlanmıştır. Ceza komutlarının biçimi UnrealIRCd içindir.** Başka bir IRC sunucusu yazılımı kullanıyorsan KLINE/ZLINE desteğini ve sözdizimini ayrıca kontrol et.

## 1. Başlamadan önce gerekenler

| Gereken | Ne işe yarar? |
|---|---|
| Linux bilgisayar veya VPS | Botun çalışacağı makine. Kesintisiz çalışması için makine açık kalmalı. |
| Terminal/SSH erişimi | Kurulum komutlarını burada çalıştırırsın. |
| Go 1.26 veya üzeri | Kaynak kodunu çalıştırılabilir programa dönüştürür. |
| Git | Projeyi GitHub'dan indirir ve günceller. |
| IRC sunucu adresi ve portu | Botun hangi IRC sunucusuna bağlanacağını belirler. |
| OPER hesabı ve şifresi | Botun sunucuda KLINE/ZLINE uygulayabilmesi için gerekir. Kanal operatörlüğü yeterli değildir. |
| Kendi IRC hesap adın | Botun yönetim komutlarını kimin kullanabileceğini belirler. |
| Seçilen sağlayıcının API anahtarı | OpenAI Moderation veya Google Cloud Natural Language erişimi sağlar. |

OPER hesabını IRC sunucusunun yöneticisi oluşturur. Bu bot OPER hesabı oluşturmaz. Hesaba kullanacağın KLINE/ZLINE yetkileri verilmiş olmalı. Bot yalnızca katıldığı kanallarda görebildiği mesajları kontrol eder; tüm ağdaki mesajları otomatik olarak göremez.

Kod bloklarında `sh` olarak gösterilen komutlar Linux terminaline, `/msg` ile başlayan komutlar ise IRC istemcisinin mesaj alanına yazılır.

## 2. OpenAI API anahtarı alma

1. Tarayıcıdan [OpenAI Platform](https://platform.openai.com/) hesabına giriş yap veya hesap oluştur.
2. Kullanacağın organizasyon/projeyi seç. Projen yoksa panelden bir proje oluşturabilirsin; örneğin `IrcModerasyonBotu` adını ver.
3. [API Keys](https://platform.openai.com/api-keys) sayfasını aç.
4. Yeni gizli anahtar oluşturma düğmesine bas: **Create new secret key**. Anahtara botu hatırlatacak bir isim ver.
5. Anahtarı oluştur ve kopyala. Yetkileri kısıtlanmış anahtar kullanıyorsan Moderations uç noktasına istek gönderme izni bulunmalı.
6. Aşağıda oluşturacağın `bilgehan.conf` dosyasının `openai.api_key` alanına yapıştır.

Anahtarın yerleştirileceği alan:

```json
"api_key": "BURAYA_KENDI_OPENAI_API_ANAHTARIN"
```

Anahtar bir şifredir; herkese açık GitHub dosyalarına, ekran görüntülerine veya IRC mesajlarına koyma. Burada kullanılan dosya sunucuda kalır ve Git tarafından yok sayılır.

Bu bot yalnızca **Moderation API** kullanır; bu hizmet ücretsizdir. Ancak istek ve token sınırları vardır. Hesabındaki **Limits** ekranından `omni-moderation` sınırlarını kontrol et. Aynı kullanıcının kısa aralıkla gönderdiği satırlar bir metin olarak değerlendirilir; OpenAI'de farklı kullanıcıların metinleri bir istekte ayrı girdiler olarak gönderilebilir. İstek sayısı azalır, fakat metinlerin toplam token tüketimi ortadan kalkmaz. Token, metnin model tarafından işlenen küçük parçalarına verilen addır; bir token her zaman bir kelimeye eşit değildir.

Resmî kaynaklar: [OpenAI API başlangıç rehberi](https://developers.openai.com/api/docs/quickstart), [Moderation API](https://developers.openai.com/api/docs/guides/moderation), [model ve kullanım sınırları](https://developers.openai.com/api/docs/models/omni-moderation-latest).

### Google Cloud seçeneği

Google için **Cloud Natural Language API** kullanılır. Perspective API 31 Aralık 2026'da kapanacağı için bu projeye eklenmemiştir. [Duyuru](https://www.perspectiveapi.com/).

1. [Google Cloud Console](https://console.cloud.google.com/) üzerinden bir proje seç veya oluştur.
2. Projenin faturalandırmasını etkinleştir; Cloud Natural Language API kullanımının [ücretlendirmesini](https://cloud.google.com/products/natural-language/pricing) incele. OpenAI'nin ücretsiz Moderation hizmetiyle aynı ücret modeli değildir.
3. API kitaplığında **Cloud Natural Language API** (`language.googleapis.com`) hizmetini etkinleştir.
4. **APIs & Services → Credentials** bölümünden bir API anahtarı oluştur. API kısıtlamasını Cloud Natural Language API ile sınırla; sunucun sabit IP kullanıyorsa IP kısıtlaması da uygulayabilirsin.
5. Kurulum sihirbazında `google` seç veya `bilgehan.google.conf.example` dosyasını kopyala. Anahtarı `google.api_key` alanına yaz. OpenAI anahtarı gerekmez.

Bot anahtarı URL'ye eklemez; `X-Goog-Api-Key` başlığında gönderir. Google REST v2 `documents:moderateText`, `MODEL_VERSION_1` kullanılır.

Google'ın moderasyon dil listesinde Türkçe için doğrulanmış destek bulunmuyor. Bot, API `languageSupported: false` döndürdüğünde varsayılan olarak sonucu kullanmaz ve puan vermez. Deneme sonuçlarını kendin değerlendirerek `google.allow_unsupported_language: true` seçebilirsin; bu ayar dil kalitesini iyileştirmez, yalnızca doğrulanmamış sonucu kullanmaya izin verir.

Sağlayıcıların kategorileri birebir aynı değildir. Google örneği `Toxic`, `Insult`, `Derogatory` ve `Violent` kullanır; ayrıca `Profanity` kuralı eklenebilir. OpenAI'deki ayrı tehdit kategorilerini Google adına taklit etmez. Google'a geçerken yalnızca `provider` alanını değiştirme; Google kural listesini de kullan. Sihirbaz doğru kuralları hazırlar.

Kaynaklar: [Google kurulum](https://docs.cloud.google.com/natural-language/docs/setup), [API anahtarı kullanımı](https://docs.cloud.google.com/docs/authentication/api-keys-use), [kategori ve dil desteği](https://docs.cloud.google.com/natural-language/docs/moderating-text), [REST v2](https://docs.cloud.google.com/natural-language/docs/reference/rest/v2/documents/moderateText).

## 3. Sunucuyu hazırlama ve projeyi indirme

Önce yardımcı programları kur:

```sh
sudo apt update
sudo apt install -y git nano ca-certificates
```

Go sürümünü kontrol et:

```sh
go version
```

Çıktıda `go1.26` veya daha yeni bir sürüm bulunmalı. `go: command not found` görürsen veya sürüm daha eskiyse [Go'nun resmî kurulum rehberinden](https://go.dev/doc/install) işletim sistemine ve işlemcine uygun sürümü kur. Kurulumdan sonra terminali tekrar açıp `go version` komutunu yeniden çalıştır.

Projeyi ev dizinine indir:

```sh
cd ~
git clone https://github.com/bilgehanars/IrcModerasyonBotu.git
cd IrcModerasyonBotu
```

Git, proje için bir klasör oluşturur. Sonraki komutları bu klasörün içindeyken çalıştır.

## 4. Ayar dosyasını oluşturma

**Projede `.env` dosyası kullanılmaz. Ayarların tamamı `bilgehan.conf` dosyasından okunur.** `OPENAI_API_KEY` ortam değişkeni tanımlamak da bu sürümde dosyanın yerini almaz.

En kolay yöntem, önce programı derleyip kurulum sihirbazını çalıştırmaktır:

```sh
go mod download
CGO_ENABLED=0 go build -o ircmoderasyonbotu .
./ircmoderasyonbotu -init -config bilgehan.conf
./ircmoderasyonbotu -config bilgehan.conf -check
```

Sihirbaz sağlayıcıyı, IRC adresini, bot nickini, OPER bilgilerini, kanalları, master hesaplarını ve API anahtarını sorar. Gerçek terminalde şifre girişleri görünmez. TLS ve deneme modu açık başlar; ayrıntılı eşikler sonradan düzenlenebilir. Mevcut dosyanın üzerine yazmaz. Yarım kalan veya geçersiz kurulumda dosya oluşturmaz. Şifre sorularına Enter ile geçmeden gerekli değeri yaz.

Elle düzenlemeyi tercih edersen OpenAI için örnek dosyayı kopyala:

```sh
cp bilgehan.conf.example bilgehan.conf
chmod 600 bilgehan.conf
nano bilgehan.conf
```

Google için `cp bilgehan.google.conf.example bilgehan.conf` kullan. Sihirbazla dosya oluşturduysan tekrar `cp` çalıştırma; doğrudan `nano bilgehan.conf` ile düzenle.

`chmod 600`, dosyayı yalnızca sahibinin okuyup yazabilmesini sağlar. Nano'da kaydetmek için **Ctrl+O**, ardından **Enter**; çıkmak için **Ctrl+X** kullan.

Dosya **JSON** biçimindedir. Alan adlarını ve metinlerin çevresindeki çift tırnakları koru. `true`/`false` değerlerini tırnak içine alma. Son alandan sonra virgül ekleme; dosyaya `#` veya `//` ile yorum yazma.

Dosyanın tam OpenAI örneği:

```json
{
  "provider": "openai",
  "irc": {
    "server": "irc.example.com:6697",
    "tls": true,
    "server_password": "",
    "nick": "BilgehanBot",
    "user": "moderasyon",
    "real_name": "Bilgehan Moderasyon Botu",
    "oper_name": "BOT_OPER_ADI",
    "oper_password": "OPER_SIFRESI",
    "channels": [
      "#sohbet"
    ],
    "owner_accounts": [
      "bilgehan"
    ],
    "reconnect_min_seconds": 3,
    "reconnect_max_seconds": 60
  },
  "openai": {
    "api_key": "OPENAI_API_ANAHTARINI_YAZ",
    "model": "omni-moderation-latest",
    "timeout_seconds": 15
  },
  "moderation": {
    "dry_run": true,
    "queue_size": 200,
    "max_message_age_seconds": 30,
    "score_window_hours": 24,
    "action_cooldown_seconds": 3600,
    "state_file": "data/scores.db",
    "reason": "Otomatik moderasyon: tekrar eden kural ihlali",
    "rules": {
      "harassment": {
        "threshold": 0.85,
        "points": 2
      },
      "harassment/threatening": {
        "threshold": 0.85,
        "points": 6
      },
      "hate": {
        "threshold": 0.85,
        "points": 4
      },
      "hate/threatening": {
        "threshold": 0.85,
        "points": 8
      },
      "violence": {
        "threshold": 0.9,
        "points": 3
      },
      "violence/graphic": {
        "threshold": 0.9,
        "points": 4
      }
    },
    "penalties": [
      {
        "points": 10,
        "command": "KLINE",
        "duration": "30m"
      },
      {
        "points": 20,
        "command": "KLINE",
        "duration": "24h"
      },
      {
        "points": 35,
        "command": "KLINE",
        "duration": "0"
      }
    ],
    "requests_per_minute": 60,
    "max_retries": 2
  },
  "google": {
    "api_key": "",
    "timeout_seconds": 15,
    "allow_unsupported_language": false
  },
  "exceptions": {
    "accounts": [],
    "hosts": []
  }
}
```

İlk çalıştırmadan önce sunucu adresini, OPER bilgilerini, kanal listesini, sahip hesap adını ve OpenAI API anahtarını kendine göre değiştir. İlk deneme için `dry_run` değeri `true` kalsın.

### Sağlayıcı: `provider`

`"openai"` veya `"google"` seçilir. Yalnızca seçilen sağlayıcının anahtarı gerekir; otomatik olarak diğer sağlayıcıya geçilmez. Sağlayıcı/anahtar değişikliği için dosyayı düzenle ve botu yeniden başlat.

### IRC bağlantısı: `irc`

| Alan | Örnekteki değer | Açıklama |
|---|---|---|
| `server` | `irc.example.com:6697` | IRC adresi ve portu birlikte yazılır. `https://` ekleme. IPv6 adresini `[2001:db8::1]:6697` biçiminde yaz. |
| `tls` | `true` | Şifreli IRC bağlantısını açar. Port sunucunun TLS portu olmalı; 6697 yaygın bir örnektir. Sertifika doğrulaması yapılır. |
| `server_password` | `""` | Sunucuya bağlanırken istenen ortak bağlantı şifresi. Sunucu istemiyorsa boş bırak. OPER veya NickServ şifresi değildir. |
| `nick` | `BilgehanBot` | Botun kanalda görünecek takma adı. |
| `user` | `moderasyon` | IRC bağlantısındaki kullanıcı/ident alanı. Boşluksuz basit bir değer kullan. |
| `real_name` | `Bilgehan Moderasyon Botu` | Botun WHOIS bilgisinde görünen açıklama. Gerçek bir kişinin adı olması gerekmez. |
| `oper_name` | `BOT_OPER_ADI` | Sunucuda tanımlanmış OPER hesabının adı. Botun nickiyle aynı olmak zorunda değildir. |
| `oper_password` | `OPER_SIFRESI` | Bu OPER hesabının şifresi. Mevcut uygulama boşluk içeren OPER bilgilerini kabul etmez. |
| `channels` | `["#sohbet"]` | Denetlenecek kanallar. Birden fazla kanal için `["#sohbet", "#genel"]` yaz. Kanal adları `#` ile başlamalı. |
| `owner_accounts` | `["bilgehan"]` | Botu yönetebilen doğrulanmış IRC hesap adları. Birden fazla hesap yazılabilir. Nick veya hostmask değildir. |
| `reconnect_min_seconds` | `3` | İlk yeniden bağlanma beklemesi, saniye. |
| `reconnect_max_seconds` | `60` | Artan yeniden bağlanma beklemesinin temel üst sınırı, saniye. Küçük rastgele gecikme nedeniyle gerçek bekleme biraz daha uzun olabilir. |

### OpenAI bağlantısı: `openai`

| Alan | Örnekteki değer | Açıklama |
|---|---|---|
| `api_key` | `OPENAI_API_ANAHTARINI_YAZ` | Platform'dan oluşturduğun gizli API anahtarı. Örnekteki yazının tamamını kendi anahtarınla değiştir. |
| `model` | `omni-moderation-latest` | Kullanılacak moderasyon modeli. İlk kurulumda bu değeri kullan. |
| `timeout_seconds` | `15` | Bir API isteğinin en fazla kaç saniye bekleneceği. 1–120 arasında olmalı. Zaman aşımında ilgili mesaja puan verilmez. |

### Google bağlantısı: `google`

| Alan | Örnek | Açıklama |
|---|---|---|
| `api_key` | `GOOGLE_CLOUD_API_ANAHTARINI_YAZ` | Natural Language API için oluşturulan Google Cloud anahtarı. `provider: "google"` olduğunda gerekir. |
| `timeout_seconds` | `15` | Tek bir Google HTTP isteğinin zaman aşımı; 1–120 saniye. |
| `allow_unsupported_language` | `false` | API dilin desteklenmediğini bildirirse sonucu kullanma. `true`, bu sonuçlara açıkça izin verir. Türkçe için deneme yapmadan canlı yaptırım açma. |

### Başlangıç muafiyetleri: `exceptions`

| Alan | Örnek | Açıklama |
|---|---|---|
| `accounts` | `["guvenilirhesap"]` | Doğrulanmış hesap adlarının mesajları API'ye gönderilmez ve puan almaz. |
| `hosts` | `["203.0.113.7", "guvenilir.example"]` | Tam host/IP muafiyeti. Wildcard ve IP aralığı kabul edilmez. |

Conf içindeki muafiyetler dosyadan yönetilir. Master komutuyla eklenenler SQLite'ta tutulur; yeniden başlatmada korunur. İki kaynak birlikte kullanılır. Master, dosyada tanımlı muafiyeti kaldırmak isterse conf dosyasını düzenlemelidir.

### Moderasyon davranışı: `moderation`

| Alan | Örnekteki değer | Açıklama |
|---|---|---|
| `requests_per_minute` | `60` | API isteklerinin yerel dakika sınırı. Tekrar denemeleri de kapsar. Sağlayıcının token/günlük kotasının yerine geçmez. 1–10.000 arasında olmalı. |
| `max_retries` | `2` | İlk denemeden sonraki ek deneme sayısı. Ağ hataları, HTTP 429 ve 5xx için kullanılır. 0–5 arasında olmalı; 0 tekrar denemeyi kapatır. |
| `dry_run` | `true` | Deneme modu. `true` iken API kontrolleri ve puan kaydı çalışır; ban komutları yalnızca loga yazılır. `false` iken ban talepleri IRC sunucusuna gönderilir. |
| `queue_size` | `200` | Gelen mesaj ve hazır grup kuyruklarının ayrı ayrı kapasitesi; bekleyen kullanıcı grubu sayısı da bu değerle sınırlandırılır. 1–10.000 arasında olmalı. Kuyruk dolarsa yeni mesaj/grup atlanabilir. |
| `max_message_age_seconds` | `30` | Grubun ilk satırından itibaren kontrol/ceza için en uzun süre. Süre dolan grup atlanır. Saniye cinsindedir. |
| `score_window_hours` | `24` | Puan hesabına katılacak geçmiş süre. Örnekte son 24 saatin ihlalleri toplanır. 1–8.760 arasında olmalı. |
| `action_cooldown_seconds` | `3600` | Aynı veya daha düşük ceza basamağının yeniden gönderilmesi için beklenecek süre. Örnekte 1 saat. Daha yüksek basamağa geçiş beklemez. Banın süresini belirlemez. |
| `state_file` | `data/scores.db` | SQLite veritabanı dosyasının yolu. Klasör ve tablolar otomatik oluşturulur; ayrı SQLite sunucusu veya elle tablo kurulumu gerekmez. |
| `reason` | `Otomatik moderasyon: tekrar eden kural ihlali` | Ban komutunda gönderilecek neden. Kısa, tek satırlık bir metin kullan. En fazla 180 bayt; Türkçe harfler birden fazla bayt olabilir. |
| `rules` | Kategori listesi | Hangi kategoriye, hangi güven eşiğinde, kaç puan verileceğini belirler. Aşağıda açıklanıyor. |
| `penalties` | Ceza basamakları | Toplam puana göre komut ve ban süresini belirler. Aşağıda açıklanıyor. |

Göreli yollar botun çalıştırıldığı klasöre göre çözülür. Örneğin proje klasöründeyken çalıştırırsan veritabanı o klasördeki `data/scores.db` dosyası olur. Conf ayarları başlangıçta okunur; dosyadaki değişiklikten sonra botu yeniden başlat. Master komutuyla izinli ayar değişiklikleri hemen uygulanır.

### Kategori eşikleri ve puanlar: `rules`

Örnek bir kategori ayarı:

```json
"harassment": {"threshold": 0.85, "points": 2}
```

`threshold`, modelin o kategori için verdiği güven skorunun alt sınırıdır. Skor `0.85` veya üzerindeyse bu kural tetiklenir. Bu sayı kullanıcının suçluluk olasılığı değildir. Eşiği yükseltmek kuralı daha zor, düşürmek daha kolay tetikler. Kabul edilen aralık 0'dan büyük ve 1'e eşit veya küçüktür.

`points`, kural tetiklendiğinde eklenecek tam sayı puandır; 1–10.000 arasında olabilir. Modelin skoru ile kullanıcının biriken puanı farklı şeylerdir.

| Kategori | Ne için kullanılır? | Örnek eşik | Eklenecek puan |
|---|---|---:|---:|
| `harassment` | Taciz/aşağılama içeren ifadeler | 0.85 | 2 |
| `harassment/threatening` | Hedefe yönelik ciddi zarar tehdidi içeren taciz | 0.85 | 6 |
| `hate` | Korunan kimlik özelliklerine yönelik nefret söylemi | 0.85 | 4 |
| `hate/threatening` | Tehdit veya ciddi zarar çağrısı içeren nefret söylemi | 0.85 | 8 |
| `violence` | Şiddet içeren anlatım | 0.90 | 3 |
| `violence/graphic` | Ayrıntılı/ağır şiddet tasviri | 0.90 | 4 |

Aynı mesaj grubu birden fazla kategoriye girerse **yalnızca en yüksek puan** eklenir. Örneğin aynı grup hem 2 hem 6 puanlık kuralı tetiklerse toplam 6 puan yazılır. Kategorilerden birini denetlemek istemiyorsan ilgili satırı kaldırabilirsin; en az bir kural bulunmalı.

Bot aynı nick/host/hesabın aynı kanaldaki satırlarını son satırdan sonra 3 saniye sessizlikte, ilk satırdan en geç 10 saniyede, 8 satırda veya 1.500 baytta kapatır. Satırları yeni satırla birleştirerek **grup başına tek puan** verir. Örneğin `ben`, `senin`, `ananı`, `sikeyim` art arda gönderilirse tek metin olur. Başka kullanıcılar ve kanallar karıştırılmaz; uzun süreli sohbet geçmişi eklenmez. OpenAI hazır grupları en çok 32 ayrı girdiyle, en çok 150 ms ek beklemeyle bir HTTP isteğine toplar. Google tek belge kabul ettiğinden gruplar ayrı isteklere, en çok dört eşzamanlı çalışan üzerinden gider. Model şaka, alıntı veya haber içeriğini de bir kategoriye yerleştirebilir; eşikleri kendi kanalındaki deneme sonuçlarına göre ayarla.

### Ceza basamakları: `penalties`

```json
{"points": 10, "command": "KLINE", "duration": "30m"}
```

| Alan | Anlamı |
|---|---|
| `points` | Bu ceza için gereken toplam puan. Pozitif tam sayı olmalı; iki basamak aynı puanı kullanamaz. |
| `command` | Uygulanacak komut: `KLINE` veya `ZLINE`. Büyük harfle yaz. |
| `duration` | Banın süresi. `"30m"` 30 dakika, `"24h"` 24 saat, `"168h"` 7 gün, `"0"` süresiz demektir. Tırnaklar gerekli. `1d` biçimi desteklenmez. Süreli ceza 1 saniye–365 gün arasında ve tam saniye olmalı. |

Örnek yapılandırmanın sonucu:

| Son 24 saatte biriken puan | Ceza |
|---|---|
| 0–9 | Ban uygulanmaz; puan kaydı devam eder. |
| 10–19 | 30 dakika KLINE |
| 20–34 | 24 saat KLINE |
| 35 ve üzeri | Süresiz KLINE |

Toplam puana uyan en yüksek basamak seçilir. Örneğin puan 8'den 22'ye çıkarsa doğrudan 24 saatlik basamak kullanılır. Ban gönderildiğinde puan otomatik sıfırlanmaz; eski ihlaller zaman penceresinden çıktıkça hesaptan düşer.

## 5. Botu derleme ve ilk çalıştırma

Proje klasöründeyken:

```sh
go mod download
CGO_ENABLED=0 go build -o ircmoderasyonbotu .
./ircmoderasyonbotu -config bilgehan.conf -check
```

İlk komut bağımlılıkları indirir. İkinci komut `ircmoderasyonbotu` adlı çalıştırılabilir dosyayı üretir. Üçüncü komut ayar dosyasının biçimini ve değerlerini kontrol eder. Başarılıysa `Yapılandırma geçerli` mesajı görülür.

**`-check` gerçek IRC bağlantısını, OPER şifresini veya API anahtarının çalıştığını doğrulamaz.** Bunlar bot çalışırken anlaşılır.

Botu başlat:

```sh
./ircmoderasyonbotu -config bilgehan.conf
```

Terminalde bağlantı ve OPER durumunu gösteren loglar görünür. OPER başarı yanıtı alınmadan kanallara katılım yapılmaz. `DRY-RUN KLINE ...` satırı bir cezanın simüle edildiğini gösterir; gerçek ban gönderilmez. Botu durdurmak için **Ctrl+C** kullan. Bu şekilde çalıştırıldığında terminal/SSH oturumu kapanınca çalışmaya devam etmesi garanti değildir; sürekli kullanım için servis bölümünü uygula.

### Sahip hesabını kontrol etme

Kendi IRC istemcinde, `owner_accounts` listesinde yazan hesabına sunucunun giriş yöntemiyle giriş yap. Ardından:

```text
/msg BilgehanBot help
/msg BilgehanBot status
```

Botun nickini değiştirdiysen komutlardaki `BilgehanBot` yerine yeni nicki yaz. Bot NOTICE mesajı ile cevap verir.

Sahip kontrolü, sunucunun gönderdiği IRCv3 `account-tag` bilgisine dayanır. Sadece `bilgehan` nickini almak yetki sağlamaz. Sunucu bu özelliği desteklemiyorsa bot güvenli master doğrulaması yapamayacağı için oturumu kapatır; moderasyon başlatılmaz.

Doğrulanmış sahip hesabının kanal mesajları denetimden muaftır. Moderasyonu deneyeceksen ikinci bir test hesabı kullan.

### Gerçek banları etkinleştirme

Deneme sonuçlarını ve sunucunun ban davranışını kontrol ettikten sonra `/msg BilgehanBot set dry_run false` ile canlı yaptırımı açabilirsin. Dosyadan yönetiyorsan botu durdur, `bilgehan.conf` içindeki `dry_run` değerini `false` yap ve yeniden başlat. Master daha önce bu ayarı değiştirdiyse SQLite değeri önceliklidir; dosyaya dönmek için `unset dry_run` kullan.

Deneme modunda puanlar veritabanına yazılır; simülasyon gerçek ban/cooldown kaydı oluşturmaz. Deneme yaptığın host/IP için aşağıdaki `reset` komutunu kullanarak bu geçmişi temizleyebilirsin.

## 6. Master komutları

`irc.owner_accounts` içindeki hesaplar master yetkisine sahiptir. Komutlar yalnızca botun özel mesajında ve sunucunun doğruladığı `account-tag` üzerinden kabul edilir. Aşağıdaki `203.0.113.7` ve hesap adlarını gerçek hedefle değiştir.

| Komut | Sonuç |
|---|---|
| `/msg BilgehanBot help` | Komutları listeler. |
| `/msg BilgehanBot status` | OPER, sağlayıcı, deneme modu, gelen mesaj ve hazır grup kuyrukları. Henüz biriken gruplar bu iki sayaca dahil değildir. |
| `/msg BilgehanBot score 203.0.113.7` | Zaman penceresi içindeki toplam puan. |
| `/msg BilgehanBot list 1` | En yüksek puanlı host/IP kayıtlarının ilk sayfası. Sayfa başına 10 kayıt. |
| `/msg BilgehanBot query 203.0.113.7 1` | Bu host/IP için kayıtlı son ihlaller: UTC zaman, kategori ve puan. SQL metni çalıştırmaz. |
| `/msg BilgehanBot reset 203.0.113.7` | Puanları temizler; ban kaldırmaz. |
| `/msg BilgehanBot forgive 203.0.113.7` | Puanları temizler; botun kayıtlı aktif banları için kaldırma talebi gönderir. |
| `/msg BilgehanBot settings` | Değiştirilebilir etkin kuralları ve ayarları gösterir; anahtar/şifre göstermez. |
| `/msg BilgehanBot set dry_run true` | Deneme modunu açar. |
| `/msg BilgehanBot set score_window_hours 48` | Puan penceresini 48 saat yapar. Daha önce temizlenmiş kayıtları geri getirmez. |
| `/msg BilgehanBot set action_cooldown_seconds 7200` | Aynı/düşük basamak için cooldown 2 saat. |
| `/msg BilgehanBot set max_message_age_seconds 45` | Mesaj yaş sınırını değiştirir. |
| `/msg BilgehanBot set reason Kanal kurali ihlali` | Ban nedenini değiştirir. |
| `/msg BilgehanBot set rule.harassment.threshold 0.90` | Mevcut OpenAI kuralının eşiğini değiştirir. |
| `/msg BilgehanBot set rule.harassment.points 3` | Mevcut kuralın puanını değiştirir. Google örneği: `rule.Toxic.points`. |
| `/msg BilgehanBot set penalty.10.duration 1h` | Mevcut 10 puanlık basamağın süresini değiştirir. |
| `/msg BilgehanBot set penalty.10.command ZLINE` | Mevcut basamağın komutunu değiştirir. Gerçek IP gereksinimi devam eder. |
| `/msg BilgehanBot unset dry_run` | SQLite'taki değişikliği siler; conf değerine döner. Diğer izinli ayar adları için de kullanılabilir. |
| `/msg BilgehanBot except list 1` | Muafiyet listesinin ilk sayfası. |
| `/msg BilgehanBot except add account guvenilirhesap` | Doğrulanmış hesabı muaf tutar. Nick değil hesap adı yaz. |
| `/msg BilgehanBot except del account guvenilirhesap` | Master tarafından eklenmiş hesap muafiyetini kaldırır. |
| `/msg BilgehanBot except add host 203.0.113.7` | Tam host/IP muafiyeti ekler. |
| `/msg BilgehanBot except del host 203.0.113.7` | Master tarafından eklenmiş host muafiyetini kaldırır. |

`exception` komutu `except` ile aynı anlama gelir. Liste/sorgu sayfaları 1'den başlar. Hedef olarak nick yerine tam host/IP kullanılır; ihlal logundaki `host=` alanından bulunabilir. Boşluklu metinler yalnızca `reason` değerinde kullanılabilir.

Ayarlar doğrulanır, uygulanır ve SQLite'a kaydedilir. **SQLite'taki master ayarları conf değerlerinin önüne geçer**; yeniden başlatmada korunur. Dosyanın değerine dönmek için `unset` kullan. Kategori değişiklikleri sağlayıcıya göre ayrı tutulur. Master hesabı listesi, anahtarlar, dosya yolları, bağlantı ayarları ve sağlayıcı IRC üzerinden değiştirilemez; conf dosyasından yönetilir.

`forgive`, yalnızca botun veritabanında `pending/requested` olarak bildiği, henüz süresi dolmamış ban taleplerini hedefler. Diğer operatörlerin banlarını arayıp kaldırmaz. IRC sunucusundan uygulanma onayı sorgulanmadığı için cevap bir **kaldırma talebidir**. Bağlantı o sırada kesildiyse aynı `forgive` komutu tekrar gönderilebilir. Sunucu sonucu oper loglarından doğrulanmalı.

Af, reset, ayar veya muafiyet değişikliğinden önce kuyruğa girmiş moderasyon sonuçları geçersiz sayılır; eski bir API yanıtı affedilen kullanıcıya tekrar puan yazmaz. Yönetim değişikliklerinin kim tarafından ve ne zaman yapıldığı SQLite'taki `admin_audit` tablosunda tutulur.

Puanlar nick yerine **tam host/IP** üzerinden tutulur. Nick değiştirince puan korunur. Host/IP değişirse yeni kayıt oluşur. Ortak host/IP kullananlar aynı puan kaydından ve host/IP yasağından etkilenebilir.

### KLINE ve ZLINE farkı

| Komut | Bu botun hedefi | Gereken bilgi |
|---|---|---|
| `KLINE` | `*@tam-host` | Mesaj önekinde görünen tam host veya IP. Cloak eşleşmesi sunucu ayarına bağlıdır. |
| `ZLINE` | `*@IP` | Mesaj önekinde gerçek IPv4/IPv6 adresi görünmeli. |

ZLINE seçildiğinde host gizlenmişse bot onu DNS ile çözerek IP tahmini yapmaz. İhlal puanı kaydedilir, ceza atlanır ve loga neden yazılır. KLINE'a otomatik dönüş yapılmaz.

KLINE ve ZLINE **yerel IRC sunucusu** yaptırımlarıdır; çok sunuculu ağın tamamına otomatik yayılmaz. Bot komutu gönderdiğinde loga bir ceza talebi yazılır; sunucunun banı gerçekten eklediğini sorgulamaz. OPER izinlerini ve sonucu sunucunun loglarından doğrula.

## 7. Sunucu açılışında otomatik başlatma: systemd

Bu bölüm systemd kullanan Linux dağıtımları içindir. Önce terminalde çalışan botu **Ctrl+C** ile durdur. Aynı botu hem terminalden hem servisten aynı anda başlatma.

Örnekte proje `~/IrcModerasyonBotu` klasörüne kurulmuştur. Servis dosyası `/opt/IrcModerasyonBotu` yolunu kullanır. Önce klasörü taşı:

```sh
cd ~
sudo mv IrcModerasyonBotu /opt/IrcModerasyonBotu
cd /opt/IrcModerasyonBotu
```

Hedefte aynı isimli klasör zaten varsa taşıma komutunu tekrar çalıştırma; mevcut klasörü kullan.

Servis için ayrı bir sistem kullanıcısı oluştur ve dosyaları ona ver:

```sh
sudo useradd --system --home /opt/IrcModerasyonBotu --shell /usr/sbin/nologin ircmod
sudo chown -R ircmod:ircmod /opt/IrcModerasyonBotu
sudo chmod 600 /opt/IrcModerasyonBotu/bilgehan.conf
sudo cp deploy/ircmoderasyonbotu.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ircmoderasyonbotu
```

`ircmod` kullanıcısı zaten varsa `useradd` adımını geç. `enable`, sonraki açılışlarda otomatik başlatmayı; `--now`, hemen başlatmayı sağlar.

| İşlem | Terminal komutu |
|---|---|
| Durumu gör | `sudo systemctl status ircmoderasyonbotu` |
| Canlı logları izle | `sudo journalctl -u ircmoderasyonbotu -f` |
| Son 50 log satırını gör | `sudo journalctl -u ircmoderasyonbotu -n 50 --no-pager` |
| Yeniden başlat | `sudo systemctl restart ircmoderasyonbotu` |
| Durdur | `sudo systemctl stop ircmoderasyonbotu` |
| Açılışta otomatik başlamayı kapat | `sudo systemctl disable ircmoderasyonbotu` |

Log ekranından çıkmak için **Ctrl+C**, durum ekranından çıkmak için **q** kullan. Log ekranından çıkmak servisi durdurmaz. `disable` tek başına çalışan servisi durdurmaz.

Servis kurulduktan sonra ayarları düzenlemek için:

```sh
sudo nano /opt/IrcModerasyonBotu/bilgehan.conf
sudo systemctl restart ircmoderasyonbotu
```

## 8. SQLite kayıtları ve yedekleme

SQLite, ayrı sunucu gerektirmeyen dosya tabanlı bir veritabanıdır. Bot ilk çalıştığında `data/scores.db` dosyasını ve tabloları oluşturur:

| Tablo | Tutulan bilgi |
|---|---|
| `users` | Host/IP kimliği, son ceza talebinin zamanı ve puan basamağı |
| `actions` | Botun ban/kaldırma talepleri, hedef maske, süre ve durum |
| `settings` | Master tarafından değiştirilen kalıcı ayarlar |
| `exceptions` | Master tarafından eklenen muafiyetler |
| `admin_audit` | Yönetim değişikliklerinin zamanı, master hesabı ve hedefi |
| `incidents` | İhlalin zamanı, kategorisi ve verilen puan |

Mesaj metinleri veritabanına kaydedilmez. Puanlar bot kapatılıp açıldığında korunur. Veritabanı okunamazsa bot sessizce boş kayıtla başlamaz; hata vererek durur.

Servis kurulumunda yedek almak için:

```sh
sudo systemctl stop ircmoderasyonbotu
sudo cp /opt/IrcModerasyonBotu/data/scores.db /opt/IrcModerasyonBotu/data/scores-backup.db
sudo systemctl start ircmoderasyonbotu
```

Bu örnek aynı isimli eski yedeğin üzerine yazar; farklı tarihler için farklı yedek adları kullan. Veritabanı henüz oluşturulmadıysa kopyalama başarısız olur.

Çalışan SQLite yanında `-wal` ve `-shm` dosyaları görülebilir; bunlar normaldir. Yukarıdaki yöntem bot durduğunda kopyalar. Çalışırken yalnızca `.db` dosyasını kopyalamak son işlemleri içermeyebilir.

## 9. Sık karşılaşılan sorunlar

| Belirti | Kontrol edilecekler |
|---|---|
| `go: command not found` | Go kurulumunu ve terminalin PATH ayarını kontrol et; terminali tekrar aç. |
| Go sürümü yetersiz | `go version` çıktısını kontrol et; en az Go 1.26 kullan. |
| `bilgehan.conf` bulunamıyor | Dosyayı örnekten kopyaladığından ve doğru klasörde olduğundan emin ol; gerekirse `-config` ile tam yol belirt. |
| Yapılandırma/JSON hatası | Çift tırnakları, virgülleri ve alan adlarını kontrol et; yorum satırlarını kaldır. |
| Bot IRC'ye bağlanamıyor | Adres, port, TLS seçimi, sertifika, sunucu şifresi ve ağ erişimini kontrol et. |
| Bot kanala girmiyor | Önce OPER başarı logunu kontrol et. Sonra kanalın anahtar/davet gibi giriş kısıtlarını kontrol et. Bu sürümde kanal anahtarı ayarı yoktur. |
| `464`, `491` veya `481` | OPER bilgileri veya IRC yetkilerini kontrol et. |
| Nick kullanımda / `433` | Botun başka bir kopyasının çalışıp çalışmadığını kontrol et veya nicki değiştir. |
| API `401` | API anahtarını ve projesini kontrol et; gerekirse yeni anahtar oluşturup dosyayı güncelle. |
| API `403` | API anahtarının ve projenin ilgili uç noktaya erişim izinlerini kontrol et. |
| API `429` | Platform'daki kullanım limitlerini, kota ve hesap durumunu kontrol et. |
| Master kontrolü / `account-tag` hatası | Listede nick yerine hesap adının yazdığını, o hesaba giriş yaptığını ve sunucunun `account-tag` desteklediğini kontrol et. Bu yetenek yoksa bot moderasyon başlatmaz. |
| Google dil destek hatası | API dili doğrulanmış olarak desteklemiyor. `allow_unsupported_language` varsayılanı sonucu atlar. Kaliteyi denemeden canlı yaptırım açma. |
| Veritabanı başka bot tarafından kullanılıyor | Aynı `state_file` ile ikinci süreç başlatılmış olabilir. Mevcut süreci/servisi kontrol et. `.lock` dosyasının varlığı tek başına süreç çalışıyor demek değildir; dosyayı silerek kilidi aşmaya çalışma. |
| Mesajlara puan yazılmıyor | Kanal listesini, API hatalarını ve eşikleri kontrol et. Sahip hesabı muaftır. Kuyruk dolan veya eskiyen mesajlar atlanır. |
| `DRY-RUN` görünüyor | Deneme modu açıktır; beklenen davranıştır. Gerçek ban için `dry_run: false` gerekir. |
| ZLINE atlanıyor | Mesaj önekinde gerçek IP görünmüyor olabilir. Gizli hosttan IP tahmini yapılmaz. |
| Ban gönderilmiş görünüyor ama uygulanmıyor | Sunucunun KLINE/ZLINE desteğini, OPER izinlerini ve tam host eşleşmesini sunucu tarafında kontrol et. |
| SQLite/dosya izin hatası | Botu çalıştıran kullanıcının `data` klasörüne yazabildiğini kontrol et. Servis kurulumunda kullanıcı `ircmod` olur. |

API hatası veya zaman aşımında ilgili gruba puan/ceza verilmez. Ağ hataları, HTTP 429 ve 5xx sınırlı sayıda yeniden denenir. 429 yanıtındaki `Retry-After` dikkate alınır; bekleme en fazla 120 saniyeye sınırlandırılır ve grubun yaş/deadline sınırı yine geçerlidir. `requests_per_minute` her HTTP isteğini sınırlar; günlük/token kotası sağlayıcı panelinden takip edilmelidir. Kuyruk ve birikmekte olan metinler bellektedir: bot kapanıp yeniden açılırsa gönderilmemiş gruplar kaybolur.

## 10. Güncelleme ve geliştirici kontrolleri

Servis kurulmuşsa güncelleme:

```sh
sudo systemctl stop ircmoderasyonbotu
cd /opt/IrcModerasyonBotu
sudo -u ircmod git pull --ff-only
sudo -u ircmod env CGO_ENABLED=0 go build -o ircmoderasyonbotu .
sudo systemctl start ircmoderasyonbotu
```

`go` komutu sistem kullanıcısının PATH'inde de bulunmalı. Bu adımda `go` bulunamıyorsa `command -v go` ile tam yolunu öğren ve derleme komutundaki `go` yerine o yolu yaz. Yeni sürümün derlemesi başarısız olursa hata çözülmeden güncellemeyi tamamlanmış sayma. Ayar veya veritabanı dosyasını yeniden oluşturma; mevcut dosyalar korunur.

Kod üzerinde değişiklik yapanlar için, yazılabilir bir proje klasöründe:

```sh
go test -race ./...
go vet ./...
```

Testler gerçek OpenAI/Google hesabına veya gerçek IRC ağına bağlanmaz; yerel test sunucularını kullanır. `-race` kontrolü için sistemde C derleyicisi gerekebilir; Ubuntu/Debian'da `sudo apt install build-essential` ile kurulabilir. Normal `CGO_ENABLED=0` bot derlemesi C derleyicisi gerektirmez.

IRC bağlantı/protokol işlemleri [girc](https://github.com/lrstanley/girc), SQLite erişimi [modernc.org/sqlite](https://modernc.org/sqlite) ile yapılır. Yeniden bağlanma beklemesi, OPER/JOIN akışı ve moderasyon kuralları bot tarafından yönetilir.

## Verilerin işlenmesi ve kaynaklar

Denetlenen kanal mesajları aynı kullanıcı/kanal için kısa süre birleştirilerek seçilen OpenAI veya Google API'ye gönderilir; uzun süreli sohbet geçmişi eklenmez. Mesaj içerikleri botun loguna veya SQLite'a yazılmaz. Host/IP, kategori ve puanlar loglarda; host/IP ile puan/ceza zamanları veritabanında bulunur. API anahtarı ve OPER şifresi bot tarafından loglanmaz.

- [OpenAI API başlangıç rehberi](https://developers.openai.com/api/docs/quickstart)
- [OpenAI API kimlik doğrulaması](https://developers.openai.com/api/reference/overview)
- [OpenAI Moderation API](https://developers.openai.com/api/docs/guides/moderation)
- [UnrealIRCd kullanıcı ve OPER komutları](https://www.unrealircd.org/docs/User_%26_Oper_commands)
- [Go kurulumu](https://go.dev/doc/install)

## Üretime geçişte doğrulanacaklar

Derleme, yarış algılayıcılı testler, provider/HTTP hata senaryoları ve yerel IRC akışları otomatik kontrol edilir. Ayrıntılar [üretim kontrol raporunda](PRODUCTION_READINESS.md) bulunur.

Kendi sunucunda OPER izinlerini, kanal girişini, `account-tag` desteğini ve KLINE/ZLINE kaldırma biçimini önce deneme ortamında doğrula. Provider anahtarlarının gerçek hesapta çalışması, Google dil davranışı ve bant/kota kapasitesi yerel testlerle kanıtlanamaz. Gerçek trafikle eşikler gözlenmeden yalnızca testlerin geçmesine dayanarak otomatik ban açma.
