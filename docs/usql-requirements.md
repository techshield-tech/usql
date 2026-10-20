# Yêu cầu bổ sung / sửa cho `techshield-tech/usql`

## Bối cảnh

`connector-ssh` gọi binary `usql` qua `pkg/srvconn/conn_usql.go` để kết nối database cho người dùng. Hiện binary được tải từ `jumpserver-dev/usql` (`Dockerfile-base`: `v0.2.3`, `utils/setup-usql.sh`: `v0.1.13`).

Fork `techshield-tech/usql` (commit `876d9c8`, module `github.com/techshield-tech/usql`) hiện gần như là upstream `xo/usql` thuần. Fork chỉ có tag `v0.0.1` và không có release nào. So với những gì connector-ssh cần, fork còn thiếu các mục dưới đây.

## Cách connector-ssh gọi usql

```
usql "<scheme>://<user>:<pass>@<host>:<port>/<db>?<params>" --variable=PROMPT1=<scheme>%R%#
```

- Biến môi trường: `PAGE=`.
- `<scheme>` là một trong: `mysql`, `postgres`, `sqlserver`, `oracle`, `clickhouse`, `dameng`.
- Kiểm tra giao thức hỗ trợ bằng `usql --drivers`, nếu lỗi thì dùng `usql -c '\drivers'`. Kết quả được dò bằng `strings.Contains(output, protocol)`.

## Yêu cầu

### 1. Che dữ liệu (data masking) — bắt buộc

- Tham số DSN: `data-masking-rules=<JSON>`.
- JSON là danh sách rule đang active, đã sắp xếp theo thứ tự ưu tiên, sinh từ `model.DataMaskingRules` của `github.com/jumpserver-dev/sdk-go/model` (xem `conn_usql.go:116-130`).
- Hiện fork **không có** code xử lý tham số này. Tham số bị bỏ qua nên dữ liệu trả về không được che.
- Cần: parse tham số, áp dụng rule lên kết quả truy vấn trước khi in ra, và xoá tham số khỏi DSN trước khi truyền cho driver.
- Cần xác nhận khi có rule: kết quả có bị che đúng cột/giá trị hay không.

### 2. Driver Dameng (`dameng`) — bắt buộc

- Không có driver `dm`/`dameng` trong `drivers/`.
- Connector khai báo `ProtocolDameng` ở `pkg/srvconn/conn.go:152` và alias scheme `dameng` ở `pkg/proxy/server_database.go:19`.
- Cần: thêm driver Dameng và đăng ký scheme `dameng` để `usql --drivers` liệt kê được.

### 3. SSL/TLS cho MySQL

Connector truyền (`conn_usql.go:108-113`):

```
tls=custom
ssl-ca=<path>
ssl-cert=<path>
ssl-key=<path>
```

- Fork không có code đăng ký TLS config cho MySQL (chỉ có ở `drivers/vertica/vertica.go:49`).
- `tls=custom` mà chưa đăng ký config sẽ lỗi, và `ssl-ca`/`ssl-cert`/`ssl-key` không phải tham số của `go-sql-driver/mysql`.
- Cần: đọc 3 tham số trên, gọi `mysql.RegisterTLSConfig("custom", cfg)` với CA/cert/key từ các đường dẫn đó, rồi dùng `tls=custom`.

### 4. Cần kiểm tra (chưa xác nhận trong fork)

- **Postgres:** `sslmode`, `sslrootcert`, `sslcert`, `sslkey` (`conn_usql.go:94-106`). Cần xác nhận `dburl` và driver `pgx` chuyển các tham số này đúng.
- **SQL Server:** `encrypt=disable` (`conn_usql.go:134`).
- **Oracle:** tham số `DBA PRIVILEGE=SYSDBA` (`conn_usql.go:139`). Cần xác nhận `go-ora` nhận đúng. Có dấu cách trong tên tham số nên phải kiểm tra việc encode URL.
- **`--drivers` / `\drivers`:** đầu ra phải chứa đủ tên scheme `mysql`, `postgres`, `sqlserver`, `oracle`, `clickhouse`, `dameng`.
- **Biến `PROMPT1`:** `--variable=PROMPT1=<scheme>%R%#` phải được áp dụng.
- **Biến môi trường `PAGE=`:** pager phải bị tắt khi biến này rỗng.

### 5. Release

- Cần tạo release có binary `usql-<tag>-linux-amd64.tar.gz` và `usql-<tag>-linux-arm64.tar.gz`, cùng bản `darwin-amd64`/`darwin-arm64` cho môi trường dev.
- Trong tarball, binary tên `usql` nằm ở thư mục gốc (xem `utils/setup-usql.sh` và `Dockerfile-base`).
- Cần có build tag/driver để bao gồm các scheme ở trên (mặc định upstream chỉ có PostgreSQL, MySQL, SQLite3, SQL Server, Oracle, CSVQ; ClickHouse cần tag `most`/`all`).
- Sau khi có release, connector-ssh sẽ đổi URL tải ở `Dockerfile-base` và `utils/setup-usql.sh` sang repo mới.

## Tham chiếu

- Bản đã có sẵn các patch trên: `github.com/jumpserver-dev/usql` (release `v0.1.13`, `v0.2.3`).
- Code phía connector: `pkg/srvconn/conn_usql.go`, `pkg/srvconn/conn.go`, `pkg/proxy/server_database.go`.

## Ghi chú về phạm vi kiểm tra

Danh sách trên được lập bằng cách đọc code và grep, chưa chạy thử binary của fork. Mục 1–3 đã xác nhận thiếu trong mã nguồn fork. Mục 4 chưa xác nhận.

## Trạng thái xử lý

Cập nhật sau khi làm mục 1 đến 5. Chi tiết kỹ thuật nằm ở W62, W63 trong `BACKLOG.md` và ở D6.

| Mục | Kết quả | Kiểm tra |
|---|---|---|
| 1. Data masking | Xong: `masking/`, nối vào `handler/` | Chạy thật với SQLite: che cột, giữ NULL, crosstab, `\gset`, JSON sai thì không kết nối |
| 2. Dameng | Xong ở mức driver và scheme (D6), tag `dameng`, nhóm `all` | `\drivers` liệt kê `dameng`. Chưa nối thử vào máy chủ DM8 thật |
| 3. MySQL TLS | Xong: `drivers/mysql/tls.go` | Unit test với chứng chỉ tự sinh. Chưa bắt tay TLS với MySQL thật |
| 4. Cần kiểm tra | `sslmode`, `sslrootcert`, `sslcert`, `sslkey`, `encrypt=disable`, `DBA PRIVILEGE` tới được driver đúng dạng. `PROMPT1` hoạt động. `PAGE=` trước đây không có tác dụng, đã sửa. `--drivers` không tồn tại, connector dùng `\drivers` thay thế | Đọc DSN do dburl sinh ra, đã thử `pgx` đọc tham số |
| 5. Release | `.github/workflows/connector.yml` | Build chéo 4 đích bằng `CGO_ENABLED=0` thành công. Workflow chưa chạy trên GitHub |
