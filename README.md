# FIFA Online 3 Server — Go + Gin

Server riêng (private server) cho game **FIFA Online 3**, viết lại hoàn toàn bằng **Go + Gin**, thay cho bản Python cũ:

- **API game + Messenger** chạy trong một tiến trình Go duy nhất, tương thích nguyên bản client FO3 (không cần sửa client, chỉ trỏ lại địa chỉ server).
- **MariaDB** lưu dữ liệu người dùng — toàn bộ state được **mã hóa AES-256-GCM** ở tầng ứng dụng; mật khẩu băm **Argon2id**.
- **Redis** làm cache, session và trạng thái online.
- **Web admin bằng React** tại `/admin/` — quản lý gần như mọi chức năng trong game: tài khoản, HLV, EP/GP, cầu thủ, HLV huấn luyện (staff), kho đồ, shop và giá, clan, xếp hạng, mùa giải, cấp PC/VIP, bảo trì, backup, log lỗi client.
- **Nginx** làm reverse proxy phía trước (không cần aaPanel — hướng dẫn này cài Nginx thuần).

> Đây là dự án fan-made phục vụ học tập và chơi cá nhân/nhóm nhỏ. Không liên kết với EA, Garena hay Nexon.

## 1. Kiến trúc tổng quan

```
Client FO3 ──HTTPS/HTTP──► Nginx :80/:443 ──► Go server 127.0.0.1:8080 ──► MariaDB
     │                                            │        └────────────► Redis
     └────────TCP trực tiếp──────────────────────►│  Messenger :28002
                                                  └─ Web admin tại /admin/
```

- Client kết nối API qua Nginx (cổng 80/443). Go chỉ nghe ở `127.0.0.1:8080`, không lộ ra ngoài.
- Messenger (chat, bạn bè, hiện diện online) là cổng **TCP 28002**, client nối **trực tiếp** tới IP/domain của server — cổng này phải mở trên tường lửa.
- Dữ liệu người dùng mã hóa bằng khóa chính `/etc/fo3/master.key`. **Mất khóa = không đọc lại được dữ liệu**, kể cả khi còn backup DB.

## 2. Yêu cầu hệ thống

| Hạng mục | Tối thiểu | Khuyến nghị |
|---|---|---|
| HĐH | Ubuntu 22.04 / Debian 12 / AlmaLinux 9 / Rocky Linux 9 | Ubuntu 24.04 hoặc AlmaLinux 9 |
| CPU/RAM | 2 vCPU / 2 GB | 4 vCPU / 4 GB |
| Đĩa | 20 GB trống | 40 GB |
| Quyền | root hoặc `sudo` | — |
| Mạng | 1 IP tĩnh; có domain riêng là lợi thế (không bắt buộc) | |

Hướng dẫn dùng `apt` cho Debian/Ubuntu và `dnf` cho Alma/Rocky. Mọi lệnh cần quyền root đều viết qua `sudo` — nếu bạn đang ở user `root` thì bỏ `sudo` đi.

## 3. Cài MariaDB (không cần aaPanel)

**Ubuntu/Debian:**

```bash
sudo apt update
sudo apt install -y mariadb-server
sudo systemctl enable --now mariadb
```

**AlmaLinux/Rocky:**

```bash
sudo dnf install -y mariadb-server
sudo systemctl enable --now mariadb
```

Tạo database và user cho game (đặt mật khẩu mạnh của riêng bạn):

```bash
sudo mysql -u root <<'SQL'
CREATE DATABASE fo3 CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'fo3app'@'localhost' IDENTIFIED BY 'MAT_KHAU_DB_CUA_BAN';
GRANT ALL PRIVILEGES ON fo3.* TO 'fo3app'@'localhost';
FLUSH PRIVILEGES;
SQL
```

Các bảng được server **tự tạo khi chạy lần đầu** (migration tích hợp sẵn) — không cần import file SQL nào.

> Lưu ý vận hành: chỉ để **một** tiến trình quản lý MariaDB. Nếu máy có aaPanel thì để aaPanel quản lý và đừng bật thêm `mariadb.service`/`mysqld.service` của distro — hai bộ giám sát cùng chạy sẽ tranh chấp khóa dữ liệu và làm DB chập chờn.

## 4. Cài Redis

**Ubuntu/Debian:** `sudo apt install -y redis-server`
**AlmaLinux/Rocky:** `sudo dnf install -y redis`

Đặt mật khẩu cho Redis (chỉ nghe localhost):

```bash
# /etc/redis/redis.conf (Ubuntu/Debian) hoặc /etc/redis.conf (Alma/Rocky)
bind 127.0.0.1
requirepass MAT_KHAU_REDIS_CUA_BAN
```

```bash
sudo systemctl enable --now redis-server   # Ubuntu/Debian
sudo systemctl enable --now redis          # Alma/Rocky
```

## 5. Khóa mã hóa và file cấu hình bí mật

```bash
sudo mkdir -p /etc/fo3
# Khóa chính AES-256 (32 byte, base64) — SAO LƯU khóa này ở nơi an toàn, tách khỏi backup DB
openssl rand -base64 32 | sudo tee /etc/fo3/master.key >/dev/null
sudo chmod 0400 /etc/fo3/master.key

# Thông tin kết nối MariaDB (DSN)
printf 'FO3_DB_DSN=fo3app:MAT_KHAU_DB_CUA_BAN@tcp(127.0.0.1:3306)/fo3?parseTime=true\n' | sudo tee /etc/fo3/db.env >/dev/null
# Mật khẩu Redis
printf 'FO3_REDIS_PASS=MAT_KHAU_REDIS_CUA_BAN\n' | sudo tee /etc/fo3/redis.env >/dev/null
sudo chmod 0400 /etc/fo3/db.env /etc/fo3/redis.env
```

## 6. Build server Go

Cài Go 1.23 trở lên:

```bash
curl -LO https://go.dev/dl/go1.23.4.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.23.4.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

Lấy mã nguồn và build:

```bash
git clone https://github.com/trinhcanh2k4/FIFA-Online-3-Server-Go.git
cd FIFA-Online-3-Server-Go
go mod tidy
go build -o fo3-server ./cmd/server
```

Bố trí thư mục chạy (mặc định server tìm dữ liệu ở `/opt/fo3-go`):

```bash
sudo mkdir -p /opt/fo3-go
sudo cp fo3-server /opt/fo3-go/
sudo cp -r gamedata /opt/fo3-go/          # các bảng dữ liệu trích xuất có sẵn trong repo
```

## 7. Dữ liệu game: `gamedata/`, `responses/`, `assets/`

| Thư mục | Nội dung | Nguồn |
|---|---|---|
| `gamedata/` | `fifadb.sqlite` (CSDL cầu thủ — bắt buộc), `formations.json`, `club_catalog.json`, các file `*_tables.json`, `fa_prices.json`, `staff_catalog.json`… | **Không kèm trong repo này** — đây là dữ liệu trích xuất từ client game. Người tự host tự trích từ bản client của mình, hoặc lấy từ thư mục `gamedata/` của bản server Python cũ đang chạy của chính mình |
| `responses/` | Các file JSON phản hồi tĩnh (`account_base.json`, `account_features.json`, `static_db_*.json`…) | Thư mục `responses/` của server Python |
| `assets/` | Ảnh: `club_emblems/`, `externalAssets/shop/`, `externalAssets/staffs/` (chân dung HLV huấn luyện)… | Từ bản cài đặt server đang chạy hoặc bộ dữ liệu client |

Đường dẫn được cấu hình qua biến môi trường (mặc định như dưới — xem mục 9):

- `FO3_GAMEDATA_DIR` (mặc định `/opt/fo3-go/gamedata`)
- `FO3_RESPONSES_DIR` (mặc định `/opt/fo3-go/responses`)
- `FO3_ASSETS_DIR` (mặc định `/opt/fo3-go/assets`)
- `FO3_ADMIN_DIR` (mặc định `/opt/fo3-go/web-admin-dist`)

## 8. Build web admin (React)

```bash
# Cài Node.js 20 LTS (ví dụ qua NodeSource)
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt install -y nodejs        # Alma/Rocky: sudo dnf install -y nodejs

cd web-admin
npm install
npm run build                     # kết quả trong web-admin/dist
sudo cp -r dist /opt/fo3-go/web-admin-dist
```

Tạo tài khoản quản trị đầu tiên (lệnh này cũng dùng để **đặt lại mật khẩu admin** khi quên):

```bash
go build -o /tmp/makeadmin ./cmd/makeadmin
sudo /tmp/makeadmin admin 'MAT_KHAU_ADMIN_CUA_BAN'
```

Đăng nhập tại `http(s)://<domain-hoặc-IP>/admin/`.

## 9. Chạy bằng systemd

Tạo `/etc/systemd/system/fo3-go.service`:

```ini
[Unit]
Description=FO3 Go server
After=network.target mariadb.service redis.service
Wants=mariadb.service redis.service

[Service]
Type=simple
WorkingDirectory=/opt/fo3-go
Environment=FO3_PUBLIC_IP=<IP_SERVER_CUA_BAN>
Environment=FO3_PUBLIC_HOST=<DOMAIN_HOAC_IP_CUA_BAN>
ExecStart=/opt/fo3-go/fo3-server
Restart=on-failure
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

- `FO3_PUBLIC_IP`: IP public của server — server trả địa chỉ này cho client để kết nối Messenger. **Phải sửa thành IP của bạn** (mặc định trong code là IP của tác giả).
- `FO3_PUBLIC_HOST`: domain của bạn; nếu chỉ có IP thì điền IP.
- Biến tùy chọn khác: `FO3_ADDR` (mặc định `127.0.0.1:8080`), `FO3_MSG_ADDR` (mặc định `:28002`).

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now fo3-go
curl http://127.0.0.1:8080/healthz
# {"mariadb":true,"ok":true,"redis":true,"service":"fo3-go"}
```

## 9b. Cấu hình địa chỉ server / Server address configuration

**English:** Every public address the server announces to clients is
configurable — the source ships with documentation placeholders
(`203.0.113.10`, `fo3.example.com`), so set these environment
variables to **your own** IP/domain before going live:

| Variable | Meaning | Default in code |
|---|---|---|
| `FO3_PUBLIC_IP` | Public IP sent to clients for the Messenger connection | `203.0.113.10` |
| `FO3_PUBLIC_HOST` | Public domain/IP of the game site; also builds the relay address `<host>:28000` announced in room matches | `fo3.example.com` |
| `FO3_GAME_HOST` | Host treated as "the game domain" (admin panel answers 404 there) | `fo3.example.com` |
| `FO3_ADMIN_HOST` | Host of the admin subdomain; requests under it skip the panel token login (Nginx basic auth is the gate) | `fo3-adm.example.com` |

Set them in the systemd unit (section 9):

```ini
Environment=FO3_PUBLIC_IP=<YOUR_PUBLIC_IP>
Environment=FO3_PUBLIC_HOST=<YOUR_DOMAIN_OR_IP>
Environment=FO3_GAME_HOST=<YOUR_DOMAIN_OR_IP>
Environment=FO3_ADMIN_HOST=fo3-adm.<YOUR_DOMAIN>
```

The admin web app also treats any hostname starting with `fo3-adm.`
as the admin subdomain. On the client side, put the same domain/IP
(bare, **no port**) into `fo3_server.ini` — see section 12.

**Tiếng Việt:** Mọi địa chỉ public server trả cho client đều cấu hình
được — mã nguồn để sẵn giá trị mẫu (`203.0.113.10`,
`fo3.example.com`), hãy đặt các biến môi trường trên thành IP/domain
**của bạn** trước khi mở server: `FO3_PUBLIC_IP` (IP public cho kết
nối Messenger), `FO3_PUBLIC_HOST` (domain/IP trang game, đồng thời là
host của địa chỉ relay `<host>:28000` trong phòng đấu),
`FO3_GAME_HOST` (host bị coi là domain game — panel admin trả 404 ở
đó), `FO3_ADMIN_HOST` (host của subdomain admin — request dưới host
này bỏ qua đăng nhập token của panel, cổng chặn là basic auth của
Nginx). Đặt chúng trong unit systemd như ví dụ trên; web admin cũng
tự nhận mọi hostname bắt đầu bằng `fo3-adm.` là subdomain admin.
Phía client, ghi cùng domain/IP đó (dạng trần, **không kèm cổng**)
vào `fo3_server.ini` — xem mục 12.

## 10. Nginx làm reverse proxy

Cài Nginx: `sudo apt install -y nginx` hoặc `sudo dnf install -y nginx`, rồi `sudo systemctl enable --now nginx`.

### Trường hợp A — Có domain riêng (dùng HTTPS, khuyến nghị)

```nginx
server {
    listen 80;
    server_name fo3.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name fo3.example.com;

    ssl_certificate     /etc/letsencrypt/live/fo3.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/fo3.example.com/privkey.pem;

    client_max_body_size 32m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        # QUAN TRỌNG: không cache API. Cache GET của API từng gây lỗi thật:
        # mua đồ không vào kho, tham gia cup không được ghi nhận, clan board mất bài.
        proxy_cache off;
        expires off;
    }

    # Chỉ cache tài nguyên tĩnh/CDN
    location ^~ /cdn/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        expires 30d;
    }
}
```

Lấy chứng chỉ miễn phí: `sudo apt install -y certbot python3-certbot-nginx && sudo certbot --nginx -d fo3.example.com`.

### Trường hợp B — Chỉ có IP, không có domain (chạy HTTP)

```nginx
server {
    listen 80 default_server;
    server_name _;

    client_max_body_size 32m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_cache off;
        expires off;
    }

    location ^~ /cdn/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        expires 30d;
    }
}
```

Đặt file vào `/etc/nginx/conf.d/fo3.conf` (hoặc `sites-available` trên Ubuntu), kiểm tra `sudo nginx -t` rồi `sudo systemctl reload nginx`.

> Có thể dùng aaPanel thay cho Nginx thuần nếu thích — khi đó tạo website trỏ về domain/IP của bạn và reverse proxy toàn bộ về `127.0.0.1:8080`. Vẫn áp dụng nguyên tắc: **tắt proxy cache cho API, chỉ cache `/cdn/`**.

## 11. Mở tường lửa

Chỉ cần mở 3 cổng: **80** (HTTP), **443** (HTTPS, nếu dùng domain) và **28002** (Messenger TCP — client nối trực tiếp). **Không** mở 8080, 3306, 6379 ra Internet.

```bash
# Ubuntu/Debian (ufw)
sudo ufw allow 80/tcp; sudo ufw allow 443/tcp; sudo ufw allow 28002/tcp

# Alma/Rocky (firewalld)
sudo firewall-cmd --permanent --add-port=80/tcp
sudo firewall-cmd --permanent --add-port=443/tcp
sudo firewall-cmd --permanent --add-port=28002/tcp
sudo firewall-cmd --reload
```

## 12. Cấu hình phía client

Trong thư mục client FO3, sửa file `fo3_server.ini`:

```ini
[Server]
IP=fo3.example.com
```

hoặc khi chỉ có IP: `IP=203.0.113.10`.

⚠️ Chỉ ghi **domain hoặc IP trần, tuyệt đối không kèm cổng** (không viết `IP=203.0.113.10:8080`). `version.dll` của client lấy nguyên chuỗi này để phân giải địa chỉ — có `:port` sẽ lỗi `COMMON_ERROR_CURL` và kẹt ở màn hình tải. Cổng API lấy theo URL của client qua Nginx (80/443), cổng Messenger client tự dùng 28002.

Launcher cũng trỏ về cùng domain/IP. Đăng nhập bằng tài khoản tạo trong web admin.

## 13. Vận hành hằng ngày

- **Quản lý game**: mọi thao tác (tạo tài khoản người chơi, cấp EP/GP, cầu thủ, HLV huấn luyện, vật phẩm, giá shop, ẩn/hiện hàng trong shop, ngày mùa giải xếp hạng & cup, cấp PC/VIP theo ngày, bật chế độ bảo trì, gửi thông báo, xem log lỗi client, backup DB, khởi động lại service) đều có trong web admin `/admin/`.
- **Backup**: trong web admin có nút tạo backup MariaDB (file `.sql.gz`). Ngoài ra hãy sao lưu thủ công định kỳ:
  ```bash
  mysqldump -u fo3app -p fo3 | gzip > ~/fo3-$(date +%F).sql.gz
  ```
  Và **luôn giữ một bản sao `/etc/fo3/master.key` ở nơi khác** — backup DB không có khóa thì không giải mã được.
- **Cập nhật phiên bản an toàn** (tránh downtime):
  ```bash
  go build -o fo3-server-new ./cmd/server     # 1. build riêng một bước, kiểm tra không lỗi
  FO3_SHADOW_ADDR=127.0.0.1:18099 ./fo3-server-new &   # 2. chạy thử ở cổng phụ
  curl http://127.0.0.1:18099/healthz          # 3. kiểm tra sống khỏe rồi kill tiến trình thử
  sudo cp fo3-server-new /opt/fo3-go/fo3-server        # 4. thay binary
  sudo systemctl restart fo3-go                        # 5. khởi động lại
  ```
  **Không** gộp build + thay file + restart thành một lệnh duy nhất nối bằng `;` — build lỗi mà vẫn thay file/restart sẽ làm server dừng hẳn.
- **Xem log**: `journalctl -u fo3-go -f`. Lỗi giao diện phía client được client gửi về và lưu trong bảng `telemetry_errors` (MariaDB), xem tại tab "Log lỗi game" của web admin.

## 14. Bảo mật

- State người dùng trong MariaDB được mã hóa **AES-256-GCM** bằng khóa chính 32 byte ở `/etc/fo3/master.key` (quyền `0400`, chỉ root đọc). Server bị xâm nhập đọc trộm DB cũng không đọc được dữ liệu. Giới hạn thật: khi server đang chạy, khóa nằm trong RAM.
- Mật khẩu người chơi và mật khẩu cấp 2: băm **Argon2id**, không lưu dạng đọc ngược được. Token phiên chỉ lưu dạng hash SHA-256.
- MariaDB và Redis chỉ nghe `localhost`. Go API chỉ nghe `127.0.0.1:8080` sau Nginx.
- Đổi mật khẩu admin web: chạy lại `makeadmin` như mục 8. Các phiên admin đang đăng nhập còn hiệu lực tối đa 12 giờ; muốn thu hồi ngay thì khởi động lại `fo3-go` (xóa token trong Redis).

## 15. Xử lý sự cố nhanh

| Triệu chứng | Kiểm tra |
|---|---|
| Client kẹt màn hình tải / `COMMON_ERROR_CURL` | `fo3_server.ini` có ghi kèm `:port` không; DNS/domain có trỏ đúng IP không |
| Vào game được nhưng chat/bạn bè không chạy | Cổng **28002** đã mở chưa; `FO3_PUBLIC_IP`/`FO3_PUBLIC_HOST` có đúng IP/domain public không |
| Mua đồ trừ tiền nhưng không thấy trong kho | Nginx đang **cache API** — tắt `proxy_cache` cho `location /` (chỉ cache `/cdn/`) |
| `/healthz` báo `mariadb:false` | MariaDB chưa chạy hoặc sai DSN trong `/etc/fo3/db.env`; kiểm tra chỉ có đúng 1 tiến trình `mysqld_safe` |
| Server không khởi động | `journalctl -u fo3-go -e`; thường do thiếu/sai `/etc/fo3/master.key` (phải là base64 của đúng 32 byte) |

## 16. Cấu trúc mã nguồn

```
cmd/
  server/       tiến trình chính (API game + Messenger + web admin)
  relay/        relay UDP cho PvP người–người (thay bản Python, stdlib thuần)
  makeadmin/    tạo/đặt lại tài khoản quản trị web
  mreset/       chạy tay một lần reset giá TTCN (kiểm thử/vận hành)
  importusers/ seedcoaches/ statedump/ dumpjson/ …  công cụ vận hành một lần
internal/
  api/          route Gin: shop, TTCN, đấu giải, cup, clan, bạn bè, admin API…
  game/         logic game và định dạng dữ liệu tương thích client
  messenger/    server Messenger TCP (giao thức Blowfish-CBC của client)
  store/        MariaDB + Redis + mã hóa AES-256-GCM/Argon2id
  config/       đọc /etc/fo3/*.env, master.key và biến môi trường FO3_*
web-admin/      giao diện quản trị React (Vite + React 19)
gamedata/       các bảng dữ liệu trích xuất sẵn (*_tables.json, catalog…)
```
