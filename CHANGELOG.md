# Değişiklikler

## v0.3.7
- **Interception uyarısı:** Interception filtre sürücüsü kuruluysa (bazı makro ve oyun araçları kurar) MX Helper açılışta bildirim verir ve Genel bakış'ta uyarı gösterir. Bu sürücü Bluetooth klavye her uykudan uyandığında bir yer harcar; yerler bitince klavye yeniden başlatana kadar çalışmaz. "Sadece ses tuşları çalışıyor" takılmasının asıl sebebi buydu.
  - Uyarıdaki **Kaldır** düğmesi Interception'ı klavye/fare sürücü listesinden çıkarır (yönetici izni ister; Windows'un kendi sürücüleri kalır). Ardından bilgisayarı yeniden başlatmak gerekir.
  - Interception kuruluyken onarım boşuna çalıştırılmaz; yeniden başlatmak gerektiği söylenir.
  - Tanılama raporu klavye/fare sınıf filtrelerini de listeler.

## v0.3.6
- **Defender yanlış alarmı (Trojan:Win32/Bearfoos.A!ml) için önlemler:** exe artık sürüm bilgisi (şirket, açıklama, sürüm) ve uygulama bildirimi (manifest) taşıyor, sembolleri silinmiyor. Onarım komutları gizlenmiş (`-EncodedCommand`) değil, `%APPDATA%\MXHelper\scripts` içindeki düz `.ps1` dosyalarıyla çalışıyor. Bu, imzasız bir programın yapay zekâ tabanlı tespitten tamamen kurtulacağının garantisi değil; tespit sürerse Microsoft'a yanlış alarm olarak bildirilmeli.

## v0.3.5
- **Düzeltme: onarım Bluetooth'u kapatıp açamıyordu.** Intel Wireless Bluetooth adaptöründe eski yöntem "desteklenmiyor" hatası veriyordu. Bluetooth artık Windows'un hızlı ayarlardaki düğmesinin kullandığı yolla kapatılıp açılıyor; olmazsa yedek olarak `pnputil` ile aygıt kapatılıp açılıyor.
- Seviye 1'de klavye düğümü yeniden başlatılamazsa (Windows "yeniden başlatma bekleniyor" derse) onarım boşa bitmiyor, aynı adımda Bluetooth kapatılıp açılıyor.

## v0.3.4
- **Düzeltme: tuş yakalama ve makrolar arayüz açıldıktan sonra çalışmıyordu.** Klavye ve fare kancaları arayüz penceresiyle aynı iş parçacığındaydı; pencere bir saniyeden uzun meşgul kalınca Windows kancayı sessizce kaldırıyordu. Kancalar artık kendi iş parçacığında.
- **Düzeltme: Mute ×3 çalışmıyordu.** Windows medya tuşlarını çoğu zaman cihaz bilgisi olmadan iletiyor; bu basışlar artık hareket için sayılıyor.
- **Klavye ve fare birlikte:** tetikleyici artık tuş + fare tuşu olabilir: `Ctrl + sol tık`, `Q + fare ileri`, `Shift + tekerlek aşağı`. Fare çiziminde sol, sağ, tekerlek yukarı/aşağı da var. Sol/sağ tık ve tekerlek, fare normal çalışmaya devam etsin diye yalnızca bir tuşla birlikte atanabilir.
- Uygulama penceresinin kendi başlık çubuğu: Windows başlığı yerine arayüze uyan çubuk; sürükleme, çift tıkla büyütme, kenarlardan boyutlandırma çalışır.
- Tarayıcı davranışları kapatıldı: F3/Ctrl+F arama, F5 yenileme, Ctrl+P yazdırma, sağ tık menüsü, fare geri/ileri ile sayfa değişmesi gibi.
- Klavye çizimi gerçek MX Keys Mini Türkçe Q düzenine göre yeniden çizildi: ISO Enter, `<` tuşu, alt sırada Ctrl · fn · Start · Alt, üst sırada tuş simgeleri ve `ins` (ses artır) tuşu, yarım yükseklikte ok tuşları.
- MX özel tuş adları gerçek klavyeden doğrulandı (F4/F5 ışık, F10 oynat/duraklat, F11 sessiz, F12 ses azalt, ins ses artır).
- Oturum çerezi porta özel: başka bir MX Helper örneği açık pencerenin oturumunu bozamaz. Bozulursa "bağlantı hatası" yerine ne yapılacağı yazar.

## v0.3.3
- Genel bakıştaki klavye çiziminde MX Keys Mini'de bastığın tuşlar canlı olarak yanar (laptop klavyesi değil, sadece MX). Tuşlar fiziksel konumlarına göre eşlenir, Türkçe Q'da da doğru yanar. Sayfa açık değilken hiçbir şey dinlenmez.

## v0.3.2
- **Çok tuşlu kombinasyonlar:** Birden fazla normal tuşa birlikte basmak tetikleyici olabiliyor (ör. `Q+E`, `Ctrl+A+S`); basma sırası önemsiz. Klavye panelindeki "Tuş ekle" ile kurulur.
- **Ne zaman çalışsın:** Her makro için *Basınca*, *Basılı tutunca* (ör. 3 sn) ya da *Art arda basınca* (ör. 3 kez, aralarında en fazla 350 ms) seçilebiliyor. Aynı tuşa her biri için ayrı makro bağlanabilir.
  - Basılı tutma ayarlı tuşa kısa basıp bırakırsan tuş normal işini yapar.
  - Art arda basmada ilk basışlar normal çalışır, makro son basışta çalışır.

## v0.3.1
- Atamalar baştan düzenlendi:
  - **Klavye** görünümü: tuşa tıklayınca sağdaki panelde ne yaptığı görünür (düzenle, dene, aç/kapa, atamayı kaldır). Boş tuşa tek tıkla kısayol, metin, program, medya tuşu ya da turbo atanır; var olan bir makro bağlanabilir.
  - **Makrolar** görünümü: bütün makrolar profile göre gruplu tabloda; satırdan dene, çoğalt, sil.
  - Makro editörü ayrı, tam genişlikte bir sayfa: solda tetikleyici, profil ve çalışma şekli, sağda eylemler. Tetikleyicisiz makro da kaydedilebilir (kapalı olarak).
  - Her görünümün adresi var (`#/macros/list`, `#/macros/edit/<id>` …).
- README ekran görüntüleri yenilendi.
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
