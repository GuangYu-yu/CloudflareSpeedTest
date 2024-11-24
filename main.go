package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/VividCortex/ewma"
	"github.com/cheggaaa/pb/v3"
	"github.com/gookit/color"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// 全局变量定义
var (
	version, versionNew string
	
	// IP相关
	TestAll bool
	IPFile  = "ip.txt"
	IPText  string
	
	// TCPing相关 
	Routines    = 200
	TCPPort     = 443  
	PingTimes   = 4
	
	// 下载测速相关
	URL         = "https://cf.xiu2.xyz/url"
	Timeout     = 10 * time.Second
	Disable     = false
	TestCount   = 10
	MinSpeed    = 0.0
	
	// HTTPing相关
	Httping           bool
	HttpingStatusCode int  
	HttpingCFColo     string
	HttpingCFColomap  *sync.Map
	
	// CSV输出相关
	InputMaxDelay    = 9999 * time.Millisecond
	InputMinDelay    = 0 * time.Millisecond  
	InputMaxLossRate = float32(1.0)
	Output           = "result.csv"
	PrintNum         = 10
)

// 添加新的常量定义
const (
    maxV4Power = 16  // 2^16 = 65536
    maxV6Power = 20  // 2^20 = 1048576
    maxTotalIPs = 500000 // 最大总IP数
)

// 添加新的变量定义
var (
    // IPv4/IPv6 测试数量相关
    v4MaxCount int = 0  // IPv4 最大测试数量
    v6MaxCount int = 0  // IPv6 最大测试数量
)

// 基础结构体定义
type PingData struct {
	IP       *net.IPAddr
	Sended   int
	Received int
	Delay    time.Duration
}

type CloudflareIPData struct {
	*PingData
	lossRate      float32
	DownloadSpeed float64
	colo          string
}

// 进度条相关
type Bar struct {
	progress progress.Model
	total    int
	current  int
	message  string
}

// 定义更多样式
var (
    // 标题样式 - 大标题
    titleStyle = lipgloss.NewStyle().
        Bold(true).
        Foreground(lipgloss.Color("#00ff00")).
        Border(lipgloss.RoundedBorder()).
        Padding(0, 1)

    // 子标题样式 - 如"开始测速"等
    subtitleStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("#5555ff")).
        Italic(true)

    // 信息样式 - 普通提示信息    
    infoStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("#7571F9"))

    // 警告样式 - 警告信息
    warnStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("#FFA500"))

    // 错误样式 - 错误信息
    errorStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("#ff0000")).
        Bold(true)

    // 结果表头样式
    headerStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("#00ffff")).
        Bold(true).
        PaddingRight(2)

    // 结果数据样式
    dataStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("#ffff00"))

    // 版本信息样式
    versionStyle = lipgloss.NewStyle().
        Foreground(lipgloss.Color("#888888")).
        Italic(true)
)

// 美化打印函数
func printTitle(format string, a ...interface{}) {
    fmt.Println(titleStyle.Render(fmt.Sprintf(format, a...)))
}

func printSubtitle(format string, a ...interface{}) {
    fmt.Println(subtitleStyle.Render(fmt.Sprintf(format, a...)))
}

func printInfo(format string, a ...interface{}) {
    fmt.Println(infoStyle.Render(fmt.Sprintf(format, a...)))
}

func printWarn(format string, a ...interface{}) {
    fmt.Println(warnStyle.Render(fmt.Sprintf(format, a...)))
}

func printError(format string, a ...interface{}) {
    fmt.Println(errorStyle.Render(fmt.Sprintf(format, a...)))
}

// 修改进度条样式
func NewBar(count int, MyStrStart, MyStrEnd string) *Bar {
	p := progress.New(
		progress.WithGradient("#7571F9", "#9681EB"),  // 紫色渐变
		progress.WithWidth(40),                        // 进度条宽度
		progress.WithDefaultGradient(),                // 默认渐变色
		progress.WithDefaultChar('━'),                 // 进度条字符
		progress.WithoutPercentage(),                  // 不显示百分比
	)
	
	return &Bar{
		progress: p,
		total:    count,
		message:  MyStrStart,
	}
}

func (b *Bar) Grow(num int, MyStrVal string) {
	b.current += num
	b.message = MyStrVal
	
	percent := float64(b.current) / float64(b.total)
	if percent > 1.0 {
		percent = 1.0
	}
	
	// 使用 bubbles 内置的进度条渲染
	prog := b.progress.ViewAs(percent)
	
	// 构建完整显示，显示当前测速进度和总带宽
	w := fmt.Sprintf(
		"\r%d/%d %s %.2f MB/s",
		b.current, b.total,    // 测试进度
		prog,                  // 进度条
		b.message,             // 当前总带宽
	)
	
	fmt.Print(w)
}

func (b *Bar) Done() {
	fmt.Println()
}

// IP处理相关
func InitRandSeed() {
	rand.Seed(time.Now().UnixNano())
}

func isIPv4(ip string) bool {
	return strings.Contains(ip, ".")
}

// IP范围结构体
type IPRanges struct {
    ips     []*net.IPAddr
    mask    string
    firstIP net.IP
    ipNet   *net.IPNet
}

func newIPRanges() *IPRanges {
    return &IPRanges{
        ips: make([]*net.IPAddr, 0),
    }
}

// 修复IP格式
func (r *IPRanges) fixIP(ip string) string {
    if i := strings.IndexByte(ip, '/'); i < 0 {
        if isIPv4(ip) {
            r.mask = "/32"
        } else {
            r.mask = "/128"
        }
        ip += r.mask
    } else {
        r.mask = ip[i:]
    }
    return ip
}

// 解析CIDR
func (r *IPRanges) parseCIDR(ip string) {
    var err error
    if r.firstIP, r.ipNet, err = net.ParseCIDR(r.fixIP(ip)); err != nil {
        log.Fatalln("ParseCIDR err", err)
    }
}

func (r *IPRanges) appendIPv4(d byte) {
    r.appendIP(net.IPv4(r.firstIP[12], r.firstIP[13], r.firstIP[14], d))
}

func (r *IPRanges) appendIP(ip net.IP) {
    r.ips = append(r.ips, &net.IPAddr{IP: ip})
}

// 获取IP范围
func (r *IPRanges) getIPRange() (minIP, hosts byte) {
    minIP = r.firstIP[15] & r.ipNet.Mask[3]
    m := net.IPv4Mask(255, 255, 255, 255)
    for i, v := range r.ipNet.Mask {
        m[i] ^= v
    }
    total, _ := strconv.ParseInt(m.String(), 16, 32)
    if total > 255 {
        hosts = 255
        return
    }
    hosts = byte(total)
    return
}

// 选择IPv4
func (r *IPRanges) chooseIPv4() {
    if r.mask == "/32" {
        r.appendIP(r.firstIP)
        return
    }
    
    minIP, hosts := r.getIPRange()
    maxIPs := int(hosts) + 1 // 默认该CIDR内的IP总数

    // 如果设置了任何 IPv4 数量限制，使用计算好的最小值
    if v4MaxCount > 0 {
        if v4MaxCount < maxIPs {
            maxIPs = v4MaxCount
        }
    } else {
        // 没有设置任何数量限制时，使用默认方式:
        // 每个 /24 段随机测速一个 IP
        maxIPs = 1
    }
    
    // 如果需要测试的IP数量小于该CIDR中的所有IP数量
    if maxIPs < int(hosts)+1 {
        // 生成所有可能的IP
        allIPs := make([]byte, int(hosts)+1)
        for i := range allIPs {
            allIPs[i] = byte(i)
        }
        // 随机打乱
        rand.Shuffle(len(allIPs), func(i, j int) {
            allIPs[i], allIPs[j] = allIPs[j], allIPs[i]
        })
        // 只取需要的数量
        for i := 0; i < maxIPs; i++ {
            r.appendIPv4(allIPs[i] + minIP)
        }
    } else {
        // 测试所有IP
        for i := 0; i <= int(hosts); i++ {
            r.appendIPv4(byte(i) + minIP)
        }
    }
    
    // 移动到下一个CIDR
    r.firstIP[14]++
    if r.firstIP[14] == 0 {
        r.firstIP[13]++
        if r.firstIP[13] == 0 {
            r.firstIP[12]++
        }
    }
}

// 选择IPv6
func (r *IPRanges) chooseIPv6() {
    if r.mask == "/128" {
        r.appendIP(r.firstIP)
        return
    }
    
    // 计算最大测试数量
    maxIPs := 1 << 8 // 默认测试 256 个 IPv6
    if v6MaxCount > 0 {
        maxIPs = v6MaxCount
    }
    
    count := 0
    ipSet := make(map[string]bool)
    
    // 随机生成不重复的IPv6地址直到达到数量限制
    for count < maxIPs && r.ipNet.Contains(r.firstIP) {
        // 随机生成最后两段
        r.firstIP[15] = randIPEndWith(255)
        r.firstIP[14] = randIPEndWith(255)
        
        // 生成完整IP并转为字符串用于去重
        targetIP := make([]byte, len(r.firstIP))
        copy(targetIP, r.firstIP)
        ipStr := net.IP(targetIP).String()
        
        // 如果是新IP则添加
        if !ipSet[ipStr] {
            ipSet[ipStr] = true
            r.appendIP(targetIP)
            count++
        }
        
        // 随机调整其他字节以生成新的IP
        for i := 13; i >= 0; i-- {
            tempIP := r.firstIP[i]
            r.firstIP[i] += randIPEndWith(255)
            if r.firstIP[i] >= tempIP {
                break
            }
        }
    }
}

func randIPEndWith(num byte) byte {
    if num == 0 {
        return byte(0)
    }
    return byte(rand.Intn(int(num)))
}

// 加载IP范围
func loadIPRanges() []*net.IPAddr {
    ranges := newIPRanges()
    if IPText != "" {
        IPs := strings.Split(IPText, ",")
        for _, IP := range IPs {
            IP = strings.TrimSpace(IP)
            if IP == "" {
                continue
            }
            ranges.parseCIDR(IP)
            if isIPv4(IP) {
                ranges.chooseIPv4()
            } else {
                ranges.chooseIPv6()
            }
        }
    } else {
        if IPFile == "" {
            IPFile = "ip.txt"
        }
        file, err := os.Open(IPFile)
        if err != nil {
            log.Fatal(err)
        }
        defer file.Close()
        scanner := bufio.NewScanner(file)
        for scanner.Scan() {
            line := strings.TrimSpace(scanner.Text())
            if line == "" {
                continue
            }
            ranges.parseCIDR(line)
            if isIPv4(line) {
                ranges.chooseIPv4()
            } else {
                ranges.chooseIPv6()
            }
        }
    }
    
    // 如果 IP 总数超过限制,随机选择
    if len(ranges.ips) > maxTotalIPs {
        rand.Shuffle(len(ranges.ips), func(i, j int) {
            ranges.ips[i], ranges.ips[j] = ranges.ips[j], ranges.ips[i]
        })
        ranges.ips = ranges.ips[:maxTotalIPs]
    }
    
    return ranges.ips
}

// Ping结构体
type Ping struct {
    wg      *sync.WaitGroup
    m       *sync.Mutex
    ips     []*net.IPAddr
    csv     PingDelaySet
    control chan bool
    bar     *Bar
}

// 延迟丢包排序
type PingDelaySet []CloudflareIPData

func (s PingDelaySet) Len() int {
    return len(s)
}

// 修改 PingDelaySet 的 Less 方法，添加延迟阈值常量
const delayTolerance = 5 * time.Millisecond // 5ms 以内的延迟差异视为相近

func (s PingDelaySet) Less(i, j int) bool {
    iRate, jRate := s[i].getLossRate(), s[j].getLossRate()
    if iRate != jRate {
        return iRate < jRate
    }
    
    // 如果延迟差异在阈值内，则随机排序
    if abs(s[i].Delay - s[j].Delay) <= delayTolerance {
        return rand.Float32() < 0.5
    }
    return s[i].Delay < s[j].Delay
}

func (s PingDelaySet) Swap(i, j int) {
    s[i], s[j] = s[j], s[i]
}

// 下载速度排序
type DownloadSpeedSet []CloudflareIPData

func (s DownloadSpeedSet) Len() int {
    return len(s)
}

func (s DownloadSpeedSet) Less(i, j int) bool {
    return s[i].DownloadSpeed > s[j].DownloadSpeed
}

func (s DownloadSpeedSet) Swap(i, j int) {
    s[i], s[j] = s[j], s[i]
}

// CloudflareIPData方法
func (cf *CloudflareIPData) getLossRate() float32 {
    if cf.lossRate == 0 {
        pingLost := cf.Sended - cf.Received
        cf.lossRate = float32(pingLost) / float32(cf.Sended)
    }
    return cf.lossRate
}

// Ping相关方法
func NewPing() *Ping {
    checkPingDefault()
    ips := loadIPRanges()
    return &Ping{
        wg:      &sync.WaitGroup{},
        m:       &sync.Mutex{},
        ips:     ips,
        csv:     make(PingDelaySet, 0),
        control: make(chan bool, Routines),
        bar:     NewBar(len(ips), "可用:", ""),
    }
}

func (p *Ping) Run() PingDelaySet {
    if len(p.ips) == 0 {
        return p.csv
    }
    if Httping {
        fmt.Printf("开始延迟测速（模式：HTTP, 端口：%d, 范围：%v ~ %v ms, 丢包：%.2f)\n", TCPPort, InputMinDelay.Milliseconds(), InputMaxDelay.Milliseconds(), InputMaxLossRate)
    } else {
        fmt.Printf("开始延迟测速（模式：TCP, 端口：%d, 范围：%v ~ %v ms, 丢包：%.2f)\n", TCPPort, InputMinDelay.Milliseconds(), InputMaxDelay.Milliseconds(), InputMaxLossRate)
    }
    for _, ip := range p.ips {
        p.wg.Add(1)
        p.control <- false
        go p.start(ip)
    }
    p.wg.Wait()
    p.bar.Done()
    sort.Sort(p.csv)
    return p.csv
}

func (p *Ping) start(ip *net.IPAddr) {
    defer p.wg.Done()
    p.tcpingHandler(ip)
    <-p.control
}

func (p *Ping) tcping(ip *net.IPAddr) (bool, time.Duration) {
    startTime := time.Now()
    var fullAddress string
    if isIPv4(ip.String()) {
        fullAddress = fmt.Sprintf("%s:%d", ip.String(), TCPPort)
    } else {
        fullAddress = fmt.Sprintf("[%s]:%d", ip.String(), TCPPort)
    }
    conn, err := net.DialTimeout("tcp", fullAddress, tcpConnectTimeout)
    if err != nil {
        return false, 0
    }
    defer conn.Close()
    duration := time.Since(startTime)
    return true, duration
}

// HTTPing相关
func (p *Ping) httping(ip *net.IPAddr) (int, time.Duration) {
    hc := http.Client{
        Timeout: time.Second * 2,
        Transport: &http.Transport{
            DialContext: getDialContext(ip),
        },
        CheckRedirect: func(req *http.Request, via []*http.Request) error {
            return http.ErrUseLastResponse
        },
    }

    // 先访问一次获得状态码和Colo信息
    {
        requ, err := http.NewRequest(http.MethodHead, URL, nil)
        if err != nil {
            return 0, 0
        }
        requ.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/98.0.4758.80 Safari/537.36")
        resp, err := hc.Do(requ)
        if err != nil {
            return 0, 0
        }
        defer resp.Body.Close()

        if HttpingStatusCode == 0 || HttpingStatusCode < 100 && HttpingStatusCode > 599 {
            if resp.StatusCode != 200 && resp.StatusCode != 301 && resp.StatusCode != 302 {
                return 0, 0
            }
        } else {
            if resp.StatusCode != HttpingStatusCode {
                return 0, 0
            }
        }

        io.Copy(io.Discard, resp.Body)

        if HttpingCFColo != "" {
            cfRay := func() string {
                if resp.Header.Get("Server") == "cloudflare" {
                    return resp.Header.Get("CF-RAY")
                }
                return resp.Header.Get("x-amz-cf-pop")
            }()
            colo := p.getColo(cfRay)
            if colo == "" {
                return 0, 0
            }
        }
    }

    // 循环测速计算延迟
    success := 0
    var delay time.Duration
    for i := 0; i < PingTimes; i++ {
        requ, err := http.NewRequest(http.MethodHead, URL, nil)
        if err != nil {
            log.Fatal("意外的错误，请报告：", err)
            return 0, 0
        }
        requ.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/98.0.4758.80 Safari/537.36")
        if i == PingTimes-1 {
            requ.Header.Set("Connection", "close")
        }
        startTime := time.Now()
        resp, err := hc.Do(requ)
        if err != nil {
            continue
        }
        success++
        io.Copy(io.Discard, resp.Body)
        _ = resp.Body.Close()
        duration := time.Since(startTime)
        delay += duration
    }

    return success, delay
}

// 下载测速相关
func getDialContext(ip *net.IPAddr) func(ctx context.Context, network, address string) (net.Conn, error) {
    var fakeSourceAddr string
    if isIPv4(ip.String()) {
        fakeSourceAddr = fmt.Sprintf("%s:%d", ip.String(), TCPPort)
    } else {
        fakeSourceAddr = fmt.Sprintf("[%s]:%d", ip.String(), TCPPort)
    }
    return func(ctx context.Context, network, address string) (net.Conn, error) {
        return (&net.Dialer{}).DialContext(ctx, network, fakeSourceAddr)
    }
}

// 添加一个结构来跟踪所有IP的实时速度
type SpeedTracker struct {
    speeds map[string]float64
    mu     sync.Mutex
}

func NewSpeedTracker() *SpeedTracker {
    return &SpeedTracker{
        speeds: make(map[string]float64),
    }
}

func (st *SpeedTracker) UpdateSpeed(ip string, speed float64) {
    st.mu.Lock()
    st.speeds[ip] = speed
    st.mu.Unlock()
}

func (st *SpeedTracker) RemoveIP(ip string) {
    st.mu.Lock()
    delete(st.speeds, ip)
    st.mu.Unlock()
}

func (st *SpeedTracker) GetTotalSpeed() float64 {
    st.mu.Lock()
    defer st.mu.Unlock()
    var total float64
    for _, speed := range st.speeds {
        total += speed
    }
    return total
}

func downloadHandler(ip *net.IPAddr, bar *Bar, st *SpeedTracker) float64 {
    client := &http.Client{
        Transport: &http.Transport{DialContext: getDialContext(ip)},
        Timeout:   Timeout,
        CheckRedirect: func(req *http.Request, via []*http.Request) error {
            if len(via) > 10 {
                return http.ErrUseLastResponse
            }
            if req.Header.Get("Referer") == URL {
                req.Header.Del("Referer")
            }
            return nil
        },
    }
    req, err := http.NewRequest("GET", URL, nil)
    if err != nil {
        return 0.0
    }

    req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/98.0.4758.80 Safari/537.36")

    response, err := client.Do(req)
    if err != nil {
        return 0.0
    }
    defer response.Body.Close()
    if response.StatusCode != 200 {
        return 0.0
    }

    timeStart := time.Now()
    timeEnd := timeStart.Add(Timeout)

    contentLength := response.ContentLength
    buffer := make([]byte, 1024)

    var (
        contentRead     int64 = 0
        timeSlice             = time.Second // 每秒更新一次速度
        lastContentRead int64 = 0
        currentSpeed    float64
    )

    // 计算实时速度
    go func() {
        for {
            time.Sleep(timeSlice)
            currentSpeed = float64(contentRead-lastContentRead) / timeSlice.Seconds()
            lastContentRead = contentRead
            
            // 更新这个IP的速度
            st.UpdateSpeed(ip.String(), currentSpeed)
            
            // 获取所有IP的总带宽
            totalSpeed := st.GetTotalSpeed()
            
            // 更新进度条显示的总带宽 (转换为MB/s)
            bar.message = fmt.Sprintf("%.2f", totalSpeed/1024/1024)
            
            if contentRead >= contentLength {
                st.RemoveIP(ip.String())
                break
            }
        }
    }()

    // 下载过程
    for contentLength != contentRead {
        bufferRead, err := response.Body.Read(buffer)
        if err != nil {
            break
        }
        contentRead += int64(bufferRead)
    }

    return currentSpeed
}

// 延迟条件过滤
func (s PingDelaySet) FilterDelay() (data PingDelaySet) {
    if InputMaxDelay > 9999*time.Millisecond || InputMinDelay < 0 {
        return s
    }
    if InputMaxDelay == 9999*time.Millisecond && InputMinDelay == 0 {
        return s
    }
    for _, v := range s {
        if v.Delay > InputMaxDelay {
            break
        }
        if v.Delay < InputMinDelay {
            continue
        }
        data = append(data, v)
    }
    return
}

// 丢包条件过滤
func (s PingDelaySet) FilterLossRate() (data PingDelaySet) {
    if InputMaxLossRate >= 1 {
        return s
    }
    for _, v := range s {
        if v.getLossRate() > InputMaxLossRate {
            break
        }
        data = append(data, v)
    }
    return
}

// 测速结果输出
func (s DownloadSpeedSet) Print() {
    if PrintNum == 0 {
        return
    }
    if len(s) <= 0 {
        printInfo("\n完整测速结果 IP 数量为 0，跳过输出结果。")
        return
    }
    
    // 表头
    headers := []string{"IP 地址", "已发送", "已接收", "丢包率", "平均延迟", "下载速度 (MB/s)", "数据中心"}
    headerRow := ""
    for _, h := range headers {
        headerRow += headerStyle.Render(h)
    }
    fmt.Println(headerRow)

    // 数据行
    dateString := convertToString(s)
    if len(dateString) < PrintNum {
        PrintNum = len(dateString)
    }
    
    for i := 0; i < PrintNum; i++ {
        row := ""
        for _, field := range dateString[i] {
            row += dataStyle.Render(fmt.Sprintf("%-15s", field))
        }
        fmt.Println(row)
    }

    if Output != "" && Output != " " {
        printInfo("\n完整测速结果已写入 %v 文件，可使用记事本/表格软件查看。\n", Output)
    }
}

func convertToString(data []CloudflareIPData) [][]string {
    result := make([][]string, 0)
    for _, v := range data {
        result = append(result, []string{
            v.IP.String(),
            strconv.Itoa(v.Sended),
            strconv.Itoa(v.Received),
            strconv.FormatFloat(float64(v.getLossRate()), 'f', 2, 32),
            strconv.FormatFloat(v.Delay.Seconds()*1000, 'f', 2, 32),
            strconv.FormatFloat(v.DownloadSpeed/1024/1024, 'f', 2, 32),
            v.colo,
        })
    }
    return result
}

// 导出结果到CSV文件
func ExportCsv(data []CloudflareIPData) {
    if Output == "" || Output == " " || len(data) == 0 {
        return
    }
    fp, err := os.Create(Output)
    if err != nil {
        log.Fatalf("创建文件[%s]失败：%v", Output, err)
        return
    }
    defer fp.Close()
    w := csv.NewWriter(fp)
    _ = w.Write([]string{"IP 地址", "已发送", "已接收", "丢包率", "平均延迟", "下载速度 (MB/s)", "数据中心"})
    _ = w.WriteAll(convertToString(data))
    w.Flush()
}

// 检查更新
func checkUpdate() {
    timeout := 10 * time.Second
    client := http.Client{Timeout: timeout}
    res, err := client.Get("https://api.xiu2.xyz/ver/cloudflarespeedtest.txt")
    if err != nil {
        return
    }
    body, err := io.ReadAll(res.Body)
    if err != nil {
        return
    }
    defer res.Body.Close()
    if string(body) != version {
        versionNew = string(body)
    }
}

// 初始化参数
func init() {
    var printVersion bool
    var help = `
CloudflareSpeedTest ` + version + `
测试 Cloudflare CDN 所有 IP 的延迟和速度，获取最快 IP (IPv4+IPv6)！
https://github.com/XIU2/CloudflareSpeedTest

参数：
    -n 200
        延迟测速线程；越多延迟测速越快，性能弱的设备 (如路由器) 请勿太高；(默认 200 最多 1000)
    -t 4
        延迟测速次数；单个 IP 延迟测速的次数；(默认 4 次)
    -dn 10
        下载测速数量；延迟测速并排序后，从最低延迟起下载测速的数量；(默认 10 个)
    -dt 10
        下载测速时间；单个 IP 下载测速最长时间，不能太短；(默认 10 秒)
    -tp 443
        指定测速端口；延迟测速/下载测速时使用的端口；(默认 443 端口)
    -url https://cf.xiu2.xyz/url
        指定测速地址；延迟测速(HTTPing)/下载测速时使用的地址，默认地址不保证可用性，建议自建；

    -httping
        切换测速模式；延迟测速模式改为 HTTP 协议，所用测试地址为 [-url] 参数；(默认 TCPing)
    -httping-code 200
        有效状态代码；HTTPing 延迟测速时网页返回的有效 HTTP 状态码，仅限一个；(默认 200 301 302)
    -cfcolo HKG,KHH,NRT,LAX,SEA,SJC,FRA,MAD
        匹配指定地区；地区名为当地机场三字码，英文逗号分隔，仅 HTTPing 模式可用；(默认 所有地区)

    -tl 200
        平均延迟上限；只输出低于指定平均延迟的 IP，各上下限条件可搭配使用；(默认 9999 ms)
    -tll 40
        平均延迟下限；只输出高于指定平均延迟的 IP；(默认 0 ms)
    -tlr 0.2
        丢包几率上限；只输出低于/等于指定丢包率的 IP，范围 0.00~1.00，0 过滤掉任何丢包的 IP；(默认 1.00)
    -sl 5
        下载速度下限；只输出高于指定下载速度的 IP，凑够指定数量 [-dn] 才会停止测速；(默认 0.00 MB/s)

    -p 10
        显示结果数量；测速后直接显示指定数量的结果，为 0 时不显示结果直接退出；(默认 10 个)
    -f ip.txt
        IP段数据文件；如路径含有空格请加上引号；支持其他 CDN IP段；(默认 ip.txt)
    -ip 1.1.1.1,2.2.2.2/24,2606:4700::/32
        指定IP段数据；直接通过参数指定要测速的 IP 段数据，英文逗号分隔；(默认 空)
    -o result.csv
        写入结果文件；如路径含有空格请加上引号；值为空时不写入文件 [-o ""]；(默认 result.csv)

    -dd
        禁用下载测速；禁用后测速结果会按延迟排序 (默认按下载速度排序)；(默认 启用)
    -all4
        测速全部的 IPv4；(IPv4 默认每 /24 段随机测速一个 IP)
    -more6
        测试更多 IPv6；(表示 -v6 18，即每个 CIDR 最多测速 2^18 即 262144 个)
    -lots6
        测试较多 IPv6；(表示 -v6 16，即每个 CIDR 最多测速 2^16 即 65536 个)
    -many6
        测试很多 IPv6；(表示 -v6 12，即每个 CIDR 最多测速 2^12 即 4096 个)
    -some6
        测试一些 IPv6；(表示 -v6 8，即每个 CIDR 最多测速 2^8 即 256 个)
    -many4
        测试一点 IPv4；(表示 -v4 12，即每个 CIDR 最多测速 2^12 即 4096 个)

    -v4
        指定 IPv4 测试数量 (2^n±m，例如 -v4 0+12 表示 2^0+12 即每个 CIDR 最多测速 13 个)
    -v6
        指定 IPv6 测试数量 (2^n±m，例如 -v6 18-6 表示 2^18-6 即每个 CIDR 最多测速 262138 个)

    -v
        打印程序版本 + 检查版本更新
    -h
        打印帮助说明
`

    var minDelay, maxDelay, downloadTime int
    var maxLossRate float64

    flag.IntVar(&Routines, "n", 200, "延迟测速线程数")
    flag.IntVar(&PingTimes, "t", 4, "延迟测速次数")
    flag.IntVar(&TestCount, "dn", 10, "下载测速数量")
    flag.IntVar(&TCPPort, "tp", 443, "指定测速端口")
    flag.StringVar(&URL, "url", defaultURL, "指定测速地址")
    
    flag.BoolVar(&Httping, "httping", false, "切换测速模式")
    flag.IntVar(&HttpingStatusCode, "httping-code", 0, "有效状态代码")
    flag.StringVar(&HttpingCFColo, "cfcolo", "", "匹配指定地区")
    
    flag.DurationVar(&InputMaxDelay, "tl", 9999*time.Millisecond, "平均延迟上限")
    flag.DurationVar(&InputMinDelay, "tll", 0, "平均延迟下限")
    flag.Float64Var(&MinSpeed, "sl", 0, "下载速度下限")
    
    flag.IntVar(&PrintNum, "p", 10, "显示结果数量")
    flag.StringVar(&IPFile, "f", "ip.txt", "IP段数据文件")
    flag.StringVar(&IPText, "ip", "", "指定IP段数据")
    flag.StringVar(&Output, "o", "result.csv", "输出结果文件")
    
    flag.BoolVar(&Disable, "dd", false, "禁用下载测速")
    flag.BoolVar(&TestAll, "allip", false, "测速全部的IP")
    
    flag.BoolVar(&printVersion, "v", false, "打印程序版本")
    flag.IntVar(&downloadTime, "dt", 10, "下载测速时间")
    flag.Float64Var(&maxLossRate, "tlr", 1, "丢包几率上限")

    // 添加新的命令行参数
    flag.BoolVar(&TestAll, "all4", false, "测速全部的 IPv4")
    flag.BoolVar(&TestMore6, "more6", false, "测试更多 IPv6 (2^18)")
    flag.BoolVar(&TestLots6, "lots6", false, "测试较多 IPv6 (2^16)")
    flag.BoolVar(&TestMany6, "many6", false, "测试很多 IPv6 (2^12)")
    flag.BoolVar(&TestSome6, "some6", false, "测试一些 IPv6 (2^8)")
    flag.BoolVar(&TestMany4, "many4", false, "测试一点 IPv4 (2^12)")
    
    // v4/v6 具体数量指定
    var v4Param, v6Param string
    flag.StringVar(&v4Param, "v4", "", "指定 IPv4 测试数量 (2^n±m)")
    flag.StringVar(&v6Param, "v6", "", "指定 IPv6 测试数量 (2^n±m)")
    
    flag.Usage = func() { fmt.Print(help) }
    flag.Parse()
    
    checkDownloadDefault()
    
    if printVersion {
        println(version)
        fmt.Println("检查版本更新中...")
        checkUpdate()
        if versionNew != "" {
            fmt.Printf("发现新版本 [%s]！请前往 [https://github.com/XIU2/CloudflareSpeedTest] 更新！", versionNew)
        } else {
            fmt.Println("当前为最新版本 [" + version + "]！")
        }
        os.Exit(0)
    }

    if MinSpeed > 0 && time.Duration(maxDelay)*time.Millisecond == InputMaxDelay {
        fmt.Println("[小提示] 在使用 [-sl] 参数时，建议搭配 [-tl] 参数，以避免因凑不够 [-dn] 数量而一直测速...")
    }
    
    InputMaxDelay = time.Duration(maxDelay) * time.Millisecond
    InputMinDelay = time.Duration(minDelay) * time.Millisecond
    InputMaxLossRate = float32(maxLossRate)
    Timeout = time.Duration(downloadTime) * time.Second
    HttpingCFColomap = MapColoMap()

    if Routines > maxRoutine {
        Routines = maxRoutine
    }

    // 检测是否支持彩色输出
    if !terminal.IsTerminal(int(os.Stdout.Fd())) {
        // 不支持时禁用颜色
        lipgloss.SetColorProfile(lipgloss.NoColor)
    }

    // 解析 v4/v6 参数
    parseVParam(v4Param, &v4Power, &v4Adjust, maxV4Power)
    parseVParam(v6Param, &v6Power, &v6Adjust, maxV6Power)
    
    // 解析 v4/v6 参数后计算最终测试数量
    calculateMaxCount()
}

func main() {
    InitRandSeed()

    // 美化标题
    printTitle("CloudflareSpeedTest %s", version)
    fmt.Println()

    // 开始延迟测速
    printSubtitle("开始延迟测速（模式：%s, 端口：%d, 范围：%v ~ %v ms, 丢包：%.2f）", 
        Httping ? "HTTP" : "TCP",
        TCPPort,
        InputMinDelay.Milliseconds(),
        InputMaxDelay.Milliseconds(),
        InputMaxLossRate,
    )
    
    pingData := NewPing().Run().FilterDelay().FilterLossRate()
    
    // 开始下载测速
    printSubtitle("\n开始下载测速（下限：%.2f MB/s, 数量：%d, 队列：%d）",
        MinSpeed,
        TestCount,
        len(pingData),
    )
    
    speedData := TestDownloadSpeed(pingData)
    
    // 输出结果
    ExportCsv(speedData)
    speedData.Print()

    // 版本更新提示
    if versionNew != "" {
        printWarn("\n发现新版本 [%s]！请前往 https://github.com/XIU2/CloudflareSpeedTest 更新！", versionNew)
    }
    
    endPrint()
}

func checkPingDefault() {
    if Routines <= 0 {
        Routines = defaultRoutines
    } else if Routines > maxRoutine {
        Routines = maxRoutine
    }
    if TCPPort <= 0 || TCPPort >= 65535 {
        TCPPort = defaultPort
    }
    if PingTimes <= 0 {
        PingTimes = defaultPingTimes
    }
}

func (p *Ping) tcpingHandler(ip *net.IPAddr) {
    recv, totalDelay := p.checkConnection(ip)
    nowAble := len(p.csv)
    if recv != 0 {
        nowAble++
    }
    p.bar.Grow(1, strconv.Itoa(nowAble))
    if recv == 0 {
        return
    }

    // 获取数据中心代码
    var colo string
    if !Httping { // 只在非 Httping 模式下额外获取数据中心代码
        // 复用 httping 方法来获取数据中心代码
        if hrecv, _ := p.httping(ip); hrecv > 0 {
            // 数据中心代码会在 httping 方法中通过 CF-RAY 或 x-amz-cf-pop 获取
            // 不需要在这里重复获取
        }
    }

    // 如果获取失败，仍然添加 IP 数据，但数据中心代码为空
    data := &PingData{
        IP:       ip,
        Sended:   PingTimes,
        Received: recv,
        Delay:    totalDelay / time.Duration(recv),
    }
    p.appendIPData(&CloudflareIPData{
        PingData: data,
        colo:     colo,
    })
}

func (p *Ping) appendIPData(data *CloudflareIPData) {
    p.m.Lock()
    defer p.m.Unlock()
    p.csv = append(p.csv, *data)
}

func (p *Ping) checkConnection(ip *net.IPAddr) (recv int, totalDelay time.Duration) {
    if Httping {
        recv, totalDelay = p.httping(ip)
        return
    }
    for i := 0; i < PingTimes; i++ {
        if ok, delay := p.tcping(ip); ok {
            recv++
            totalDelay += delay
        }
    }
    return
}

// 添加辅助函数计算时间差的绝对值
func abs(d time.Duration) time.Duration {
    if d < 0 {
        return -d
    }
    return d
}

// 修改 TestDownloadSpeed 函数，对延迟相近的IP分组打乱
func TestDownloadSpeed(ipSet utils.PingDelaySet) (speedSet utils.DownloadSpeedSet) {
    checkDownloadDefault()
    if Disable {
        return utils.DownloadSpeedSet(ipSet)
    }
    if len(ipSet) <= 0 {
        fmt.Println("\n[信息] 延迟测速结果 IP 数量为 0，跳过下载测速。")
        return
    }
    
    // 将延迟相近的IP分组
    groups := make([][]CloudflareIPData, 0)
    currentGroup := []CloudflareIPData{ipSet[0]}
    
    for i := 1; i < len(ipSet); i++ {
        if abs(ipSet[i].Delay - ipSet[i-1].Delay) <= delayTolerance {
            // 延迟相近，加入当前组
            currentGroup = append(currentGroup, ipSet[i])
        } else {
            // 延迟差异较大，创建新组
            if len(currentGroup) > 0 {
                groups = append(groups, currentGroup)
            }
            currentGroup = []CloudflareIPData{ipSet[i]}
        }
    }
    // 添加最后一组
    if len(currentGroup) > 0 {
        groups = append(groups, currentGroup)
    }
    
    // 对每组内的IP随机打乱
    for i := range groups {
        rand.Shuffle(len(groups[i]), func(j, k int) {
            groups[i][j], groups[i][k] = groups[i][k], groups[i][j]
        })
    }
    
    // 重新组合所有IP
    shuffledIPs := make([]CloudflareIPData, 0, len(ipSet))
    for _, group := range groups {
        shuffledIPs = append(shuffledIPs, group...)
    }
    
    // 开始下载测速
    testNum := TestCount
    if len(shuffledIPs) < TestCount || MinSpeed > 0 {
        testNum = len(shuffledIPs)
    }
    if testNum < TestCount {
        TestCount = testNum
    }

    fmt.Printf("开始下载测速（下限：%.2f MB/s, 数量：%d, 队列：%d）\n", MinSpeed, TestCount, testNum)
    bar := utils.NewBar(TestCount, "", "")
    
    for i := 0; i < testNum; i++ {
        speed := downloadHandler(shuffledIPs[i].IP)
        shuffledIPs[i].DownloadSpeed = speed
        if speed >= MinSpeed*1024*1024 {
            bar.Grow(1, "")
            speedSet = append(speedSet, shuffledIPs[i])
            if len(speedSet) == TestCount {
                break
            }
        }
    }
    
    bar.Done()
    if len(speedSet) == 0 {
        speedSet = utils.DownloadSpeedSet(shuffledIPs)
    }
    sort.Sort(speedSet)
    return
}

var OutRegexp = regexp.MustCompile(`[A-Z]{3}`)

func MapColoMap() *sync.Map {
    if HttpingCFColo == "" {
        return nil
    }
    colos := strings.Split(strings.ToUpper(HttpingCFColo), ",")
    colomap := &sync.Map{}
    for _, colo := range colos {
        colomap.Store(colo, colo)
    }
    return colomap
}

func (p *Ping) getColo(b string) string {
    if b == "" {
        return ""
    }
    out := OutRegexp.FindString(b)
    if HttpingCFColomap == nil {
        return out
    }
    _, ok := HttpingCFColomap.Load(out)
    if ok {
        return out
    }
    return ""
}

// CloudflareIPData 的 toString 方法
func (cf *CloudflareIPData) toString() []string {
    result := make([]string, 6)
    result[0] = cf.IP.String()
    result[1] = strconv.Itoa(cf.Sended)
    result[2] = strconv.Itoa(cf.Received)
    result[3] = strconv.FormatFloat(float64(cf.getLossRate()), 'f', 2, 32)
    result[4] = strconv.FormatFloat(cf.Delay.Seconds()*1000, 'f', 2, 32)
    result[5] = strconv.FormatFloat(cf.DownloadSpeed/1024/1024, 'f', 2, 32)
    return result
}

// 是否打印测试结果
func NoPrintResult() bool {
    return PrintNum == 0
}

// 是否输出到文件
func noOutput() bool {
    return Output == "" || Output == " "
}

func endPrint() {
    if NoPrintResult() {
        return
    }
    if runtime.GOOS == "windows" {
        fmt.Printf("按下 回车键 或 Ctrl+C 退出。")
        fmt.Scanln()
    }
}

const (
    tcpConnectTimeout = time.Second * 1
    maxRoutine        = 1000
    defaultRoutines   = 200
    defaultPort       = 443
    defaultPingTimes  = 4
    bufferSize        = 1024
    defaultURL        = "https://cf.xiu2.xyz/url"
    defaultTimeout    = 10 * time.Second
    defaultDisableDownload = false
    defaultTestNum    = 10
    defaultMinSpeed   float64 = 0.0
)

func checkDownloadDefault() {
    if URL == "" {
        URL = defaultURL
    }
    if Timeout <= 0 {
        Timeout = defaultTimeout
    }
    if TestCount <= 0 {
        TestCount = defaultTestNum
    }
    if MinSpeed <= 0.0 {
        MinSpeed = defaultMinSpeed
    }
}

// 解析 2^n±m 格式的参数
func parseVParam(param string, power, adjust *int, maxPower int) {
    if param == "" {
        return
    }
    
    parts := strings.Split(param, "+")
    if len(parts) == 2 {
        *power, _ = strconv.Atoi(parts[0])
        *adjust, _ = strconv.Atoi(parts[1])
    } else {
        parts = strings.Split(param, "-")
        if len(parts) == 2 {
            *power, _ = strconv.Atoi(parts[0])
            adj, _ := strconv.Atoi(parts[1])
            *adjust = -adj
        } else {
            *power, _ = strconv.Atoi(param)
        }
    }
    
    // 检查是否超过最大值
    if *power > maxPower {
        *power = maxPower
    }
}

// 添加计算最终测试数量的函数
func calculateMaxCount() {
    // 计算 IPv4 最大测试数量
    v4Counts := make([]int, 0)
    
    // 添加 -all4 的数量 (2^32)
    if TestAll {
        v4Counts = append(v4Counts, 1<<32)
    }
    
    // 添加 -many4 的数量 (2^12)
    if TestMany4 {
        v4Counts = append(v4Counts, 1<<12)
    }
    
    // 添加 -v4 参数的数量
    if v4Power > 0 {
        count := 1 << v4Power
        if v4Adjust > 0 {
            count += v4Adjust
        } else {
            count -= -v4Adjust
        }
        v4Counts = append(v4Counts, count)
    }
    
    // 如果有任何 IPv4 相关参数，取最小值
    if len(v4Counts) > 0 {
        v4MaxCount = v4Counts[0]
        for _, count := range v4Counts {
            if count < v4MaxCount {
                v4MaxCount = count
            }
        }
    }

    // 计算 IPv6 最大测试数量
    v6Counts := make([]int, 0)
    
    // 添加各种 IPv6 参数的数量
    if TestMore6 {
        v6Counts = append(v6Counts, 1<<18) // 2^18
    }
    if TestLots6 {
        v6Counts = append(v6Counts, 1<<16) // 2^16
    }
    if TestMany6 {
        v6Counts = append(v6Counts, 1<<12) // 2^12
    }
    if TestSome6 {
        v6Counts = append(v6Counts, 1<<8)  // 2^8
    }
    
    // 添加 -v6 参数的数量
    if v6Power > 0 {
        count := 1 << v6Power
        if v6Adjust > 0 {
            count += v6Adjust
        } else {
            count -= -v6Adjust
        }
        v6Counts = append(v6Counts, count)
    }
    
    // 如果有任何 IPv6 相关参数，取最小值
    if len(v6Counts) > 0 {
        v6MaxCount = v6Counts[0]
        for _, count := range v6Counts {
            if count < v6MaxCount {
                v6MaxCount = count
            }
        }
    }
}
