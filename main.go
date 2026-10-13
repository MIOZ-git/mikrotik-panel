package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var staticFS embed.FS

type Config struct {
	PanelUser     string
	PanelSalt     []byte
	PanelPassHash []byte
	JWTSecret     []byte

	MikrotikAddr string
	MikrotikUser string
	MikrotikPass string

	DataFile string

	PingHosts  []string
	PingPeriod time.Duration
}

var (
	cfg      *Config
	cfgMutex sync.RWMutex

	pingMutex sync.RWMutex
	pingCache map[string]PingResult
)

type PingResult struct {
	Host      string    `json:"host"`
	Alive     bool      `json:"alive"`
	Latency   int64     `json:"latency_ms"`
	Timestamp time.Time `json:"timestamp"`
	Error     string    `json:"error,omitempty"`
}

// ============================================================================
// MikroTik API client
// ============================================================================

type MTClient struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
}

func mtDial(addr, user, pass string) (*MTClient, error) {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	c := &MTClient{
		conn: conn,
		r:    bufio.NewReader(conn),
		w:    bufio.NewWriter(conn),
	}
	if err := c.login(user, pass); err != nil {
		conn.Close()
		return nil, fmt.Errorf("login: %w", err)
	}
	return c, nil
}

func (c *MTClient) Close() error {
	return c.conn.Close()
}

func writeLen(w *bufio.Writer, n int) error {
	un := uint32(n)
	switch {
	case un < 0x80:
		return w.WriteByte(byte(un))
	case un < 0x4000:
		un |= 0x8000
		_, err := w.Write([]byte{byte(un >> 8), byte(un)})
		return err
	case un < 0x200000:
		un |= 0xC00000
		_, err := w.Write([]byte{byte(un >> 16), byte(un >> 8), byte(un)})
		return err
	case un < 0x10000000:
		un |= 0xE0000000
		_, err := w.Write([]byte{byte(un >> 24), byte(un >> 16), byte(un >> 8), byte(un)})
		return err
	default:
		if err := w.WriteByte(0xF0); err != nil {
			return err
		}
		_, err := w.Write([]byte{byte(un >> 24), byte(un >> 16), byte(un >> 8), byte(un)})
		return err
	}
}

func readLen(r *bufio.Reader) (int, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	switch {
	case b&0x80 == 0:
		return int(b), nil
	case b&0xC0 == 0x80:
		b2, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		return int(b&0x3F)<<8 | int(b2), nil
	case b&0xE0 == 0xC0:
		buf := make([]byte, 2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0, err
		}
		return int(b&0x1F)<<16 | int(buf[0])<<8 | int(buf[1]), nil
	case b&0xF0 == 0xE0:
		buf := make([]byte, 3)
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0, err
		}
		return int(b&0x0F)<<24 | int(buf[0])<<16 | int(buf[1])<<8 | int(buf[2]), nil
	case b == 0xF0:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0, err
		}
		return int(buf[0])<<24 | int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3]), nil
	}
	return 0, errors.New("invalid length")
}

func writeString(w *bufio.Writer, s string) error {
	if err := writeLen(w, len(s)); err != nil {
		return err
	}
	_, err := w.WriteString(s)
	return err
}

func readString(r *bufio.Reader) (string, error) {
	n, err := readLen(r)
	if err != nil {
		return "", err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func (c *MTClient) send(words []string) error {
	for _, word := range words {
		if err := writeString(c.w, word); err != nil {
			return err
		}
	}
	if err := writeString(c.w, ""); err != nil {
		return err
	}
	return c.w.Flush()
}

func (c *MTClient) readSentence() ([]string, error) {
	var words []string
	for {
		w, err := readString(c.r)
		if err != nil {
			return nil, err
		}
		if w == "" {
			return words, nil
		}
		words = append(words, w)
	}
}

func (c *MTClient) login(user, pass string) error {
	if err := c.send([]string{"/login", "=name=" + user, "=password=" + pass}); err != nil {
		return err
	}
	words, err := c.readSentence()
	if err != nil {
		return err
	}
	if len(words) > 0 && words[0] == "!trap" {
		return fmt.Errorf("authentication failed")
	}
	if len(words) > 0 && words[0] == "!done" {
		for _, w := range words {
			if strings.HasPrefix(w, "=ret=") {
				challenge := strings.TrimPrefix(w, "=ret=")
				return c.loginWithChallenge(user, pass, challenge)
			}
		}
		return nil
	}
	return fmt.Errorf("unexpected login response: %v", words)
}

func (c *MTClient) loginWithChallenge(user, pass, challenge string) error {
	chBytes, err := hex.DecodeString(challenge)
	if err != nil {
		return fmt.Errorf("decode challenge: %w", err)
	}
	h := md5.New()
	h.Write([]byte{0})
	h.Write([]byte(pass))
	h.Write(chBytes)
	response := hex.EncodeToString(h.Sum(nil))

	if err := c.send([]string{"/login", "=name=" + user, "=response=00" + response}); err != nil {
		return err
	}
	words, err := c.readSentence()
	if err != nil {
		return err
	}
	if len(words) > 0 && words[0] == "!done" {
		return nil
	}
	return fmt.Errorf("challenge login failed")
}

func sentenceToMap(words []string) map[string]string {
	m := map[string]string{}
	for _, w := range words {
		if strings.HasPrefix(w, "=") {
			kv := strings.SplitN(w[1:], "=", 2)
			if len(kv) == 2 {
				m[kv[0]] = kv[1]
			} else if len(kv) == 1 {
				m[kv[0]] = ""
			}
		}
	}
	return m
}

func (c *MTClient) Run(args ...string) ([]map[string]string, error) {
	if err := c.send(args); err != nil {
		return nil, err
	}
	var result []map[string]string
	for {
		words, err := c.readSentence()
		if err != nil {
			return nil, err
		}
		if len(words) == 0 {
			continue
		}
		switch words[0] {
		case "!re":
			result = append(result, sentenceToMap(words[1:]))
		case "!done":
			return result, nil
		case "!trap":
			m := sentenceToMap(words[1:])
			return nil, fmt.Errorf("trap: %s", m["message"])
		case "!fatal":
			return nil, fmt.Errorf("fatal: %v", words)
		}
	}
}

func (c *MTClient) Exec(args ...string) error {
	_, err := c.Run(args...)
	return err
}

// ============================================================================
// Токены (HMAC)
// ============================================================================

func signToken(user string) (string, error) {
	exp := time.Now().Add(8 * time.Hour).Unix()
	payload := fmt.Sprintf("%s|%d", user, exp)
	mac := hmac.New(sha256.New, cfg.JWTSecret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return base64.URLEncoding.EncodeToString([]byte(payload + "|" + sig)), nil
}

func verifyToken(token string) (string, error) {
	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid token")
	}
	user, expStr, sig := parts[0], parts[1], parts[2]
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", fmt.Errorf("expired")
	}
	mac := hmac.New(sha256.New, cfg.JWTSecret)
	mac.Write([]byte(user + "|" + expStr))
	expected := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
		return "", fmt.Errorf("invalid signature")
	}
	return user, nil
}

// ============================================================================
// Пароли
// ============================================================================

func hashPassword(pass string, salt []byte) []byte {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(pass))
	for i := 0; i < 10000; i++ {
		h.Write(h.Sum(nil))
	}
	return h.Sum(nil)
}

func verifyPassword(pass string, salt, hash []byte) bool {
	return subtle.ConstantTimeCompare(hashPassword(pass, salt), hash) == 1
}

// ============================================================================
// Middleware
// ============================================================================

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("panel_token")
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if _, err := verifyToken(cookie.Value); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// ============================================================================
// Handlers
// ============================================================================

func handleIndex(w http.ResponseWriter, r *http.Request) {
	data, _ := staticFS.ReadFile("index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		User string `json:"user"`
		Pass string `json:"pass"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	cfgMutex.RLock()
	userOK := req.User == cfg.PanelUser
	salt := cfg.PanelSalt
	hash := cfg.PanelPassHash
	cfgMutex.RUnlock()

	if !userOK || !verifyPassword(req.Pass, salt, hash) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})
		return
	}
	token, err := signToken(req.User)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "panel_token", Value: token, Path: "/",
		HttpOnly: true, MaxAge: 8 * 3600, SameSite: http.SameSiteLaxMode,
	})
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "panel_token", Value: "", Path: "/", MaxAge: -1})
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func handleMe(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie("panel_token")
	user, _ := verifyToken(cookie.Value)
	json.NewEncoder(w).Encode(map[string]interface{}{"user": user})
}

func mikrotikConnect() (*MTClient, error) {
	cfgMutex.RLock()
	defer cfgMutex.RUnlock()
	return mtDial(cfg.MikrotikAddr, cfg.MikrotikUser, cfg.MikrotikPass)
}

// --- Devices ---

func handleDevices(w http.ResponseWriter, r *http.Request) {
	c, err := mikrotikConnect()
	if err != nil {
		http.Error(w, "mikrotik connect: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer c.Close()

	leases, err := c.Run("/ip/dhcp-server/lease/print")
	if err != nil {
		http.Error(w, "leases: "+err.Error(), http.StatusInternalServerError)
		return
	}
	arp, _ := c.Run("/ip/arp/print")
	arpByMAC := map[string]map[string]string{}
	for _, m := range arp {
		if mac := m["mac-address"]; mac != "" {
			arpByMAC[mac] = m
		}
	}
	result := []map[string]string{}
	for _, m := range leases {
		mac := m["mac-address"]
		host := m["host-name"]
		if host == "" {
			host = "none"
		}
		iface := ""
		if a, ok := arpByMAC[mac]; ok {
			iface = a["interface"]
		}
		result = append(result, map[string]string{
			"hostname":  host,
			"ip":        m["address"],
			"mac":       mac,
			"status":    m["status"],
			"interface": iface,
			"last-seen": m["last-seen"],
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// --- Wireless ---

type WirelessClient struct {
	Interface string `json:"interface"`
	MAC       string `json:"mac"`
	Signal    int    `json:"signal"`
	SignalRaw string `json:"signal_raw"`
	TxRate    string `json:"tx_rate"`
	Hostname  string `json:"hostname"`
	IP        string `json:"ip"`
}

// parseSignal извлекает число из строки "-45@6Mbps" или "-45dBm@6Mbps"
func parseSignal(s string) int {
	cut := len(s)
	if idx := strings.Index(s, "@"); idx > 0 && idx < cut {
		cut = idx
	}
	if idx := strings.Index(s, "dBm"); idx > 0 && idx < cut {
		cut = idx
	}
	if cut <= 0 {
		return 0
	}
	if v, err := strconv.Atoi(s[:cut]); err == nil {
		return v
	}
	return 0
}

func handleWireless(w http.ResponseWriter, r *http.Request) {
	c, err := mikrotikConnect()
	if err != nil {
		http.Error(w, "mikrotik connect: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer c.Close()

	reg, err := c.Run("/interface/wireless/registration-table/print")
	if err != nil {
		http.Error(w, "wireless: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Сопоставляем с DHCP leases
	leases, _ := c.Run("/ip/dhcp-server/lease/print")
	leaseByMAC := map[string]map[string]string{}
	for _, m := range leases {
		if mac := m["mac-address"]; mac != "" {
			leaseByMAC[strings.ToLower(mac)] = m
		}
	}

	clients := []WirelessClient{}
	for _, m := range reg {
		mac := m["mac-address"]
		sigRaw := m["signal-strength"]
		sig := parseSignal(sigRaw)

		host := "none"
		ip := ""
		if l, ok := leaseByMAC[strings.ToLower(mac)]; ok {
			if l["host-name"] != "" {
				host = l["host-name"]
			}
			ip = l["address"]
		}

		clients = append(clients, WirelessClient{
			Interface: m["interface"],
			MAC:       mac,
			Signal:    sig,
			SignalRaw: sigRaw,
			TxRate:    m["tx-rate"],
			Hostname:  host,
			IP:        ip,
		})
	}

	// Сортировка: лучший сигнал сверху
	for i := 0; i < len(clients); i++ {
		for j := i + 1; j < len(clients); j++ {
			if clients[j].Signal > clients[i].Signal {
				clients[i], clients[j] = clients[j], clients[i]
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clients)
}

// --- WAN status ---

func handleWanStatus(w http.ResponseWriter, r *http.Request) {
	c, err := mikrotikConnect()
	if err != nil {
		http.Error(w, "mikrotik connect: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer c.Close()

	routes, err := c.Run("/ip/route/print", "?dst-address=0.0.0.0/0")
	if err != nil {
		http.Error(w, "routes: "+err.Error(), http.StatusInternalServerError)
		return
	}

	ifaces, _ := c.Run("/interface/print")
	ifaceState := map[string]string{}
	for _, i := range ifaces {
		ifaceState[i["name"]] = i["running"]
	}

	type WanInfo struct {
		Comment   string `json:"comment"`
		Gateway   string `json:"gateway"`
		Interface string `json:"interface"`
		Active    bool   `json:"active"`
		Disabled  bool   `json:"disabled"`
		Running   bool   `json:"running"`
		Distance  string `json:"distance"`
	}

	wanMap := map[string]string{
		"ether1": "wan1",
		"ether2": "wan2",
	}

	wans := []WanInfo{}
	for _, m := range routes {
		iface := m["vrf-interface"]
		if iface == "" {
			iface = extractIface(m["gateway"])
		}
		comment := wanMap[iface]
		if comment == "" {
			comment = m["comment"]
		}

		running := ifaceState[iface] == "true"

		wans = append(wans, WanInfo{
			Comment:   comment,
			Gateway:   m["gateway"],
			Interface: iface,
			Active:    m["active"] == "true",
			Disabled:  m["disabled"] == "true",
			Running:   running,
			Distance:  m["distance"],
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"wans": wans,
	})
}

func extractIface(gw string) string {
	if idx := strings.Index(gw, "%"); idx >= 0 {
		return gw[idx+1:]
	}
	return ""
}

// --- WAN switch ---

func handleWanSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Target != "wan1" && req.Target != "wan2" {
		http.Error(w, "invalid target", http.StatusBadRequest)
		return
	}

	c, err := mikrotikConnect()
	if err != nil {
		http.Error(w, "mikrotik connect: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer c.Close()

	routes, err := c.Run("/ip/route/print", "?dst-address=0.0.0.0/0")
	if err != nil {
		http.Error(w, "routes: "+err.Error(), http.StatusInternalServerError)
		return
	}

	wanMap := map[string]string{
		"ether1": "wan1",
		"ether2": "wan2",
	}

	var targetID, otherID string
	for _, m := range routes {
		iface := m["vrf-interface"]
		if iface == "" {
			iface = extractIface(m["gateway"])
		}
		comment := wanMap[iface]
		if comment == "" {
			comment = m["comment"]
		}

		if comment == req.Target {
			targetID = m[".id"]
		} else if comment == "wan1" || comment == "wan2" {
			otherID = m[".id"]
		}
	}

	if targetID == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": false, "message": "Маршрут " + req.Target + " не найден",
		})
		return
	}

	if otherID != "" {
		c.Exec("/ip/route/disable", "=.id="+otherID)
	}
	c.Exec("/ip/route/enable", "=.id="+targetID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "active": req.Target,
	})
}

// --- System resources ---

func handleSystem(w http.ResponseWriter, r *http.Request) {
	c, err := mikrotikConnect()
	if err != nil {
		http.Error(w, "mikrotik connect: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer c.Close()

	res, err := c.Run("/system/resource/print")
	if err != nil || len(res) == 0 {
		http.Error(w, "resource error", http.StatusInternalServerError)
		return
	}
	m := res[0]

	cpuLoad, _ := strconv.Atoi(m["cpu-load"])
	freeMem := parseSize(m["free-memory"])
	totalMem := parseSize(m["total-memory"])
	usedMem := uint64(0)
	memPct := 0
	if totalMem > freeMem {
		usedMem = totalMem - freeMem
		memPct = int(100 * usedMem / totalMem)
	}

	freeHdd := parseSize(m["free-hdd-space"])
	totalHdd := parseSize(m["total-hdd-space"])
	usedHdd := uint64(0)
	hddPct := 0
	if totalHdd > freeHdd {
		usedHdd = totalHdd - freeHdd
		hddPct = int(100 * usedHdd / totalHdd)
	}

	var usbFree, usbTotal uint64
	disks, _ := c.Run("/disk/print")
	for _, d := range disks {
		if strings.Contains(d["slot"], "part") {
			usbFree = parseSize(d["free"])
			usbTotal = parseSize(d["size"])
			break
		}
	}
	usbPct := 0
	if usbTotal > 0 && usbTotal > usbFree {
		usbPct = int(100 * (usbTotal - usbFree) / usbTotal)
	}

	result := map[string]interface{}{
		"version":         m["version"],
		"board":           m["board-name"],
		"architecture":    m["architecture-name"],
		"uptime":          m["uptime"],
		"cpu_load":        cpuLoad,
		"cpu_frequency":   m["cpu-frequency"],
		"cpu_count":       m["cpu-count"],
		"memory_used_mb":  bytesToMB(usedMem),
		"memory_total_mb": bytesToMB(totalMem),
		"memory_pct":      memPct,
		"flash_used_mb":   bytesToMB(usedHdd),
		"flash_total_mb":  bytesToMB(totalHdd),
		"flash_pct":       hddPct,
		"usb_used_mb":     bytesToMB(usbTotal - usbFree),
		"usb_total_mb":    bytesToMB(usbTotal),
		"usb_pct":         usbPct,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// --- Logs ---

func handleLogs(w http.ResponseWriter, r *http.Request) {
	c, err := mikrotikConnect()
	if err != nil {
		http.Error(w, "mikrotik connect: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer c.Close()

	logs, err := c.Run("/log/print")
	if err != nil {
		http.Error(w, "logs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	result := []map[string]string{}
	for _, m := range logs {
		topics := m["topics"]
		if strings.Contains(topics, "critical") ||
			strings.Contains(topics, "error") ||
			strings.Contains(topics, "warning") {
			result = append(result, map[string]string{
				"time":    m["time"],
				"topics":  topics,
				"message": m["message"],
			})
		}
	}
	if len(result) > 50 {
		result = result[len(result)-50:]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// --- Reboot ---

func handleReboot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c, err := mikrotikConnect()
	if err != nil {
		http.Error(w, "mikrotik connect: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer c.Close()

	go func() {
		defer func() { recover() }()
		c.Run("/system/reboot")
	}()

	time.Sleep(500 * time.Millisecond)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// --- Ping status ---

func handlePingStatus(w http.ResponseWriter, r *http.Request) {
	pingMutex.RLock()
	defer pingMutex.RUnlock()
	result := []PingResult{}
	for _, h := range cfg.PingHosts {
		if pr, ok := pingCache[h]; ok {
			result = append(result, pr)
		} else {
			result = append(result, PingResult{Host: h, Alive: false, Error: "no data"})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// --- Change password ---

func handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		OldPass string `json:"old_pass"`
		NewPass string `json:"new_pass"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(req.NewPass) < 8 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "пароль минимум 8 символов"})
		return
	}

	cfgMutex.Lock()
	defer cfgMutex.Unlock()

	if !verifyPassword(req.OldPass, cfg.PanelSalt, cfg.PanelPassHash) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "старый пароль неверный"})
		return
	}

	newSalt := make([]byte, 16)
	rand.Read(newSalt)
	cfg.PanelSalt = newSalt
	cfg.PanelPassHash = hashPassword(req.NewPass, newSalt)

	if err := saveConfig(); err != nil {
		log.Printf("save config: %v", err)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// ============================================================================
// Ping worker
// ============================================================================

func pingWorker() {
	ticker := time.NewTicker(cfg.PingPeriod)
	defer ticker.Stop()

	doPing := func() {
		c, err := mikrotikConnect()
		if err != nil {
			log.Printf("ping: connect error: %v", err)
			return
		}
		defer c.Close()

		for _, host := range cfg.PingHosts {
			res, err := c.Run("/ping", "=address="+host, "=count=1")
			pr := PingResult{Host: host, Timestamp: time.Now()}
			if err != nil {
				pr.Alive = false
				pr.Error = err.Error()
			} else if len(res) > 0 {
				m := res[0]
				if m["status"] == "timeout" {
					pr.Alive = false
					pr.Error = "timeout"
				} else {
					pr.Alive = true
					t := strings.TrimSuffix(m["time"], "ms")
					if lat, err := strconv.ParseInt(t, 10, 64); err == nil {
						pr.Latency = lat
					}
				}
			}
			pingMutex.Lock()
			pingCache[host] = pr
			pingMutex.Unlock()
		}
	}

	doPing()
	for range ticker.C {
		doPing()
	}
}

// ============================================================================
// Утилиты
// ============================================================================

func parseSize(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	mult := uint64(1)
	switch {
	case strings.HasSuffix(s, "GiB"):
		mult = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "GiB")
	case strings.HasSuffix(s, "MiB"):
		mult = 1024 * 1024
		s = strings.TrimSuffix(s, "MiB")
	case strings.HasSuffix(s, "KiB"):
		mult = 1024
		s = strings.TrimSuffix(s, "KiB")
	case strings.HasSuffix(s, "B"):
		s = strings.TrimSuffix(s, "B")
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return uint64(f * float64(mult))
}

func bytesToMB(b uint64) float64 {
	return float64(b) / (1024 * 1024)
}

// ============================================================================
// persisted config
// ============================================================================

type persistedConfig struct {
	PanelUser     string `json:"panel_user"`
	PanelSalt     string `json:"panel_salt"`
	PanelPassHash string `json:"panel_pass_hash"`
}

func saveConfig() error {
	if cfg.DataFile == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DataFile), 0755); err != nil {
		return err
	}
	data := persistedConfig{
		PanelUser:     cfg.PanelUser,
		PanelSalt:     hex.EncodeToString(cfg.PanelSalt),
		PanelPassHash: hex.EncodeToString(cfg.PanelPassHash),
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	return os.WriteFile(cfg.DataFile, b, 0600)
}

func loadConfig() (persistedConfig, bool) {
	if cfg.DataFile == "" {
		return persistedConfig{}, false
	}
	b, err := os.ReadFile(cfg.DataFile)
	if err != nil {
		return persistedConfig{}, false
	}
	var p persistedConfig
	if err := json.Unmarshal(b, &p); err != nil {
		return persistedConfig{}, false
	}
	return p, true
}

// ============================================================================
// Main
// ============================================================================

func main() {
	panelUser := getenv("PANEL_USER", "admin")
	panelPass := getenv("PANEL_PASS", "admin")
	jwtSecret := getenv("JWT_SECRET", "")
	pingHostsStr := getenv("PING_HOSTS", "8.8.8.8,5.255.255.77")
	pingPeriodSec, _ := strconv.Atoi(getenv("PING_PERIOD", "30"))

	if jwtSecret == "" {
		jwtSecret = fmt.Sprintf("secret-%d", time.Now().UnixNano())
	}

	cfg = &Config{
		PanelUser:    panelUser,
		JWTSecret:    []byte(jwtSecret),
		MikrotikAddr: getenv("MIKROTIK_ADDR", "172.17.0.1:8728"),
		MikrotikUser: getenv("MIKROTIK_USER", "api_user"),
		MikrotikPass: getenv("MIKROTIK_PASS", ""),
		DataFile:     getenv("DATA_FILE", "/data/panel.json"),
		PingHosts:    strings.Split(pingHostsStr, ","),
		PingPeriod:   time.Duration(pingPeriodSec) * time.Second,
	}
	if cfg.PingPeriod == 0 {
		cfg.PingPeriod = 30 * time.Second
	}

	pingCache = map[string]PingResult{}

	if p, ok := loadConfig(); ok {
		cfg.PanelUser = p.PanelUser
		salt, _ := hex.DecodeString(p.PanelSalt)
		hash, _ := hex.DecodeString(p.PanelPassHash)
		cfg.PanelSalt = salt
		cfg.PanelPassHash = hash
		log.Printf("Loaded persisted config")
	} else {
		salt := make([]byte, 16)
		rand.Read(salt)
		cfg.PanelSalt = salt
		cfg.PanelPassHash = hashPassword(panelPass, salt)
	}

	go pingWorker()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		handleIndex(w, r)
	})
	mux.HandleFunc("/login", handleLogin)
	mux.HandleFunc("/logout", handleLogout)
	mux.HandleFunc("/api/me", authMiddleware(handleMe))
	mux.HandleFunc("/api/devices", authMiddleware(handleDevices))
	mux.HandleFunc("/api/wireless", authMiddleware(handleWireless))
	mux.HandleFunc("/api/wan/status", authMiddleware(handleWanStatus))
	mux.HandleFunc("/api/wan/switch", authMiddleware(handleWanSwitch))
	mux.HandleFunc("/api/system", authMiddleware(handleSystem))
	mux.HandleFunc("/api/logs", authMiddleware(handleLogs))
	mux.HandleFunc("/api/reboot", authMiddleware(handleReboot))
	mux.HandleFunc("/api/ping", authMiddleware(handlePingStatus))
	mux.HandleFunc("/api/change-password", authMiddleware(handleChangePassword))

	addr := getenv("LISTEN", ":8000")
	log.Printf("Starting MikroTik panel on %s", addr)
	log.Printf("MikroTik API: %s (user: %s)", cfg.MikrotikAddr, cfg.MikrotikUser)
	log.Printf("Ping: %v every %v", cfg.PingHosts, cfg.PingPeriod)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}