# MX Helper

**Logitech MX Keys Mini için Windows yardımcı programı.** Bluetooth bağlantısı takılınca klavyeyi kendisi onarır. Arka ışığı kurallarla yönetir. Klavye kombinasyonlarına, fare yan tuşlarına ve MX'in özel tuşlarına makro bağlamanı sağlar: turbo (basılı tuttukça bas-çek), aç/kapa döngü veya kaydedilmiş tuş dizisi.

> A small Windows tray app for the Logitech MX Keys Mini: fixes the Bluetooth "only media keys work" hang, controls the backlight over HID++, and adds hotkey / mouse-button / MX-key macros with turbo (hold-to-repeat), toggle loops and recording. No dependencies, single exe.

![Genel bakış](docs/genel.png)

<p>
  <img src="docs/atamalar.png" width="49%" alt="Atamalar: tuşa tıkla, ne yapacağını seç">
  <img src="docs/editor.png" width="49%" alt="Makro editörü">
</p>

## Özellikler

### Takılma onarımı
Bazen klavye uykudan uyanınca Windows'un Bluetooth sürücüsü normal tuşların geldiği kanala yeniden abone olmuyor. O zaman sadece ses tuşları çalışıyor. Bu durumda:

- **Mute tuşuna 3 kez bas.** Program Bluetooth'u onarır ve bu sırada 3 kez açılıp kapanan sesi eski haline getirir.
- Program bu durumu kendisi de fark edebilir: klavyeden uzun süre bir şey gelmedikten sonra yalnızca ses tuşları gelirse bildirim gösterir. Ayarlardan, sormadan onarmasını da seçebilirsin.
- Onarım üç seviyeden oluşur:
  1. Klavyenin HID-over-GATT servisini yeniden başlatır.
  2. Bluetooth adaptörünü kapatıp açar.
  3. Bluetooth servisleri ile adaptörü birlikte sıfırlar.

  Hangi seviyenin işe yaradığını kaydeder ve bir dahaki sefere doğrudan o seviyeden başlar.

### Atamalar ve makrolar
**Klavye** görünümünde MX Keys Mini'nin çizimindeki bir tuşa tıklarsın: atanmışsa ne yaptığını görürsün (düzenle, dene, atamayı kaldır); boşsa birkaç tıkla kısayol, metin, program, medya tuşu ya da turbo atarsın. **Makrolar** görünümü bütün makroları profile göre listeler; bir satıra tıklayınca tam sayfa editör açılır. Program profilleri sayesinde aynı tuş her oyunda başka bir şey yapabilir.

![Makrolar](docs/makrolar.png)

| | |
|---|---|
| **Tetikleyiciler** | `ctrl+alt+k`, `f13`, `win+shift+s`… · `mouse4`, `mouse5`, `middle` · `mx:0x0108` (MX özel tuşları) |
| **Bir kez çalıştır** | Basınca eylemleri çalıştırır; istenirse N kez tekrarlar |
| **Basılı tuttukça tekrarla** | Tuş basılı kaldığı sürece eylemleri belirlediğin aralıkla tekrarlar (turbo tık, turbo tuş) |
| **Bas başlat / bas durdur** | İlk basışta döngüyü başlatır, ikinci basışta durdurur |
| **Kayıt** | Tuşları, fare tıklamalarını ve istersen fare hareketini beklemeleriyle kaydeder; beklemeleri sonra toplu hızlandırıp rastgeleleştirebilirsin |
| **Eylemler** | tuş/kısayol, metin yaz, tık ve çift tık, fareyi taşı, tekerlek, tuşu ya da fare tuşunu basılı tut/bırak, bekle (sabit ya da rastgele), program/site aç, komut, HTTP isteği, arka ışık, oyun modu, onarım |
| **Program profilleri** | Makro sadece seçtiğin program öndeyken çalışır, diğer zamanlarda tuş normal davranır |
| **Paylaşma** | Makroları JSON olarak dışa/içe aktar |
| **Acil durdurma** | `ctrl+alt+pause` çalışan tüm makroları durdurur |

### Oyun modu
Windows tuşunu, Alt+Tab'ı ve Alt+F4'ü kilitler. Elle ya da seçtiğin oyun öndeyken kendiliğinden açılır.

### Arka ışık
Otomatik, manuel ya da kapalı. Elle seçtiğin seviye yeniden başlatınca da korunur. Saate göre (ör. 23:00–07:00 kısık) ve programa göre (ör. VLC açıkken kapalı) kurallar yazılabilir. Pil belirlediğin yüzdenin altına inince ışık kapanır.

![Aydınlatma](docs/isik.png)

### Tepsi simgesi ve güncellemeler
Tepsideki simge pil seviyesini gösterir ve rengini değiştirir (yeşil, sarı, kırmızı; bağlı değilken gri). Yeni bir sürüm yayınlandığında program bunu fark eder; bildirimle sorar ya da ayarlara göre kendisi yükleyip yeniden başlar. İndirilen dosya SHA-256 ile doğrulanmadan yüklenmez.

## Kurulum

1. [Releases](../../releases) sayfasından `MXHelper.exe`'yi indir ve yazılabilir bir klasöre koy (ör. Belgeler; `Program Files` olmasın, yoksa kendini güncelleyemez).
2. Çalıştır. Simge görev çubuğunun sağ altındaki bildirim alanına yerleşir. Programı ilk açtığında ayar penceresi de kendiliğinden açılır.
3. **Ayarlar → Başlangıç → "Windows açılışında yönetici olarak başlat"** seçeneğini aç. Bir kerelik yönetici izni (UAC onayı) ister. Bu seçenek açıkken:
   - Onarım her seferinde yönetici izni sormaz.
   - Yönetici olarak çalışan oyunlarda da makrolar çalışır.

Simgeye sol tıklayınca ayar penceresi açılır, sağ tıklayınca hızlı menü açılır. Ayarlar ve günlükler `%APPDATA%\MXHelper\` klasöründe tutulur.

## Nasıl çalışır

- **Klavyeyle iletişim:** Logitech'in HID++ 2.0 protokolünü Bluetooth LE üzerinden kullanır (rapor `0x11`, cihaz indeksi `0xFF`). Pil için `0x1004`, arka ışık için `0x1982`, özel tuşlar için `0x1B04` özelliklerini kullanır.
- **Takılmayı algılama:** Windows'un raw input arayüzüyle hangi tuş olayının MX'ten geldiğini ayırt eder. Takılmanın belirtisi şudur: klavyeden medya tuşları geliyor ama normal tuşlar hiç gelmiyor.
- **Makro tetikleme:** Klavye ve fare kombinasyonlarını düşük seviyeli kancalarla (`WH_KEYBOARD_LL` / `WH_MOUSE_LL`) yakalar. Kancalar sadece bir makro bunları gerektirdiğinde kurulur. Programın kendi gönderdiği tuşlar işaretlidir, böylece kancalar onları tekrar yakalamaz.
- **Arayüz:** Programın içindeki bir web sunucusu yalnızca `127.0.0.1` adresinden ulaşılabilir ve rastgele bir erişim anahtarıyla korunur. Sayfa, programın kendi penceresinde WebView2 ile gösterilir (Windows 11'de hazır gelir; yoksa Edge'e döner).
- **Güncelleme:** GitHub'daki son sürümü kontrol eder; `MXHelper.exe`'yi SHA-256 özetiyle doğrulayıp çalışan exe'nin yerine koyar.
- **Bağımlılıklar:** Yalnızca Go standart kütüphanesi ve Win32 çağrıları kullanılır; cgo yoktur.

## Derleme

Go 1.24 veya daha yeni bir sürüm gerekir.

```bash
make build VERSION=v0.3.1            # Linux / macOS'tan Windows için çapraz derleme
powershell -File build.ps1 -Version v0.3.1   # Windows'ta
```

`v*` biçiminde bir etiket gönderdiğinde GitHub Actions exe'yi derleyip Release'e ekler.

## Notlar

- **Oyunlar:** Turbo ve makrolar bazı çevrim içi oyunların kurallarına aykırı olabilir; hile koruma yazılımları (anti-cheat) bu tür programlara tepki verebilir. Kullanmadan önce ilgili oyunun kurallarına bak.
- **Logitech yazılımları:** Logi Options+ ya da G HUB açıkken özel tuşlar ve arka ışık ayarları bu programla çakışabilir.
- **Desteklenen cihazlar:** Program yalnızca MX Keys Mini'de geliştirildi. Bluetooth üzerinden HID++ 2.0 konuşan başka Logitech klavyelerde de çalışabilir; `Ayarlar → Cihaz → Cihaz adı içerir` alanını değiştirerek deneyebilirsin.
- Bir sorun bildirirken `Ayarlar → Tanılama raporu` çıktısını da ekle.

Bu proje Logitech ile bağlantılı değildir. "Logitech", "MX Keys" ve "Logi Options+" Logitech'in ticari markalarıdır.

## Lisans

MIT
