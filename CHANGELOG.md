# Değişiklikler

## v0.3.1
- Elle seçilen arka ışık (arayüz, tray, makro ya da klavyenin kendi tuşları) artık hatırlanıyor: yeniden başlatınca geri gelir, bir sonraki kural değişimine kadar geçerli kalır. Önceden her başlatmada gece kuralı ışığı düşük seviyeye çekiyordu.
- Parlaklık çubukları tıklanan seviyeye doğru tek tek yanıp sönerek ilerliyor, klavye yanıt verene kadar takılmış görünmüyor.

## v0.3.0
- Yeni arayüz: koyu "oyun yazılımı" tasarımı, üst çubukta profil seçici, oyun modu düğmesi, pil ve bağlantı durumu.
- Atamalar: tıklanabilir MX Keys Mini klavyesi ve fare çizimi. Tuşa tıkla, atanmışsa açılır, değilse makro oluşur. Ctrl/Alt/Shift/Win'e tıklayarak kombinasyon kurulur. F tuşlarında MX özel işlevi ya da F tuşu seçilir.
- Program profilleri: makrolar programa göre gruplanır, varsayılandan gelenler ayrıca gösterilir; profil ekleme/silme, çalışan programlardan seçme.
- Makro editörü: ikonlu eylem paleti, sürükle-bırak sıralama, seçili eylemin arkasına ekleme, eylem çoğaltma, geri al (Ctrl+Z), Ctrl+S ile kaydet, Del ile eylem silme, tetikleyici çakışma uyarısı, makro süresi tahmini, hazır kısayollar (kopyala, medya, kilitle…).
- Yeni eylemler: fareyi taşı (mutlak/göreli, "konumu al"), tekerlek (dikey/yatay), çift tık, rastgele bekleme (`ms_max`), oyun modu aç/kapa.
- Kayıt: fare hareketi kaydı (yol olarak tek satırda gösterilir), beklemeleri toplu ayarla / hızlandır / rastgeleleştir / sil.
- Makroları JSON olarak içe ve dışa aktarma.
- Oyun modu: Windows tuşu, Alt+Tab, Alt+F4 kilidi; elle ya da seçilen oyun öndeyken otomatik. Tepsi menüsünde de var.
- Tepsi simgesi pil seviyesini gösterir: doluluk + renk (yeşil > %30, sarı > %15, kırmızı), bağlı değilken gri; açık/koyu görev çubuğuna uyar.
- Tek program: arayüz artık MX Helper'ın kendi penceresinde (WebView2) açılır; görev çubuğunda tek simge, koyu başlık çubuğu, yüksek DPI desteği. WebView2 yoksa Edge'e döner. Tray'e tekrar tıklamak yeni pencere açmaz, mevcut olanı öne getirir.
- Exe ikonu.
- Otomatik güncelleme: GitHub'daki son sürümü açılışta ve 6 saatte bir kontrol eder; bildirimle sorar, otomatik yükler ya da kapalı. SHA-256 doğrulanmadan yüklemez, sonra kendini yeniden başlatır.
- Tepsi menüsü yenilendi: Windows koyu temasına uyar, en üstte renkli pil ikonlu durum satırı, sade gruplama ("Gelişmiş" alt menüsü).

## v0.2.0
- Arayüz: Edge uygulama penceresinde açılan ayar ekranı (Genel, Makrolar, Arka ışık, Gelişmiş).
- Makro tetikleyicileri: klavye kombinasyonları (`ctrl+alt+k`, `f13`…), fare yan tuşları (`mouse4`, `mouse5`, `middle`) ve MX özel tuşları (`mx:0x…`).
- Çalışma şekilleri: bir kez, basılı tuttukça tekrarla (turbo), bas başlat / bas durdur.
- Makro kaydı: tuş ve fare tıklamalarını beklemeleriyle kaydedip tekrar oynatma.
- Yeni eylemler: `click`, `mousedown`/`mouseup`, `keydown`/`keyup`, `hold_ms`.
- Programa özel makrolar, "öndeki programı seç" düğmesi.
- Tüm makroları durduran acil kısayol (varsayılan `ctrl+alt+pause`).
- v0.1 `config.json` biçimi otomatik olarak yeni biçime taşınır.

## v0.1.0
- Takılma onarımı (Mute ×3, otomatik algılama, 3 seviyeli Bluetooth onarımı).
- Arka ışık kuralları, pil takibi, MX özel tuşlarına makro.
