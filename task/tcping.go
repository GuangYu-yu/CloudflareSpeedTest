// tcping.go
package task

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/XIU2/CloudflareSpeedTest/utils"
)

const (
	tcpConnectTimeout = time.Second * 1
	maxRoutine        = 1000
	defaultRoutines   = 200
	defaultPort       = 443
	defaultPingTimes  = 4
)

var (
	Routines      = defaultRoutines
	TCPPort   int = defaultPort
	PingTimes int = defaultPingTimes
)

type Ping struct {
	wg       *sync.WaitGroup
	m        *sync.Mutex
	ips      []*net.IPAddr
	csv      utils.PingDelaySet
	control  chan bool
	bar      *utils.Bar
	position int // 当前测试的位置
}

func checkPingDefault() {
	if Routines <= 0 {
		Routines = defaultRoutines
	}
	if TCPPort <= 0 || TCPPort >= 65535 {
		TCPPort = defaultPort
	}
	if PingTimes <= 0 {
		PingTimes = defaultPingTimes
	}
}

func NewPing() *Ping {
	checkPingDefault()
	ips := loadIPRanges()
	return &Ping{
		wg:       &sync.WaitGroup{},
		m:        &sync.Mutex{},
		ips:      ips,
		csv:      make(utils.PingDelaySet, 0),
		control:  make(chan bool, Routines),
		bar:      utils.NewBar(len(ips), "可用:", ""),
		position: 0,
	}
}

// RunBatch 运行一批测试，返回是否还有更多IP需要测试
func (p *Ping) RunBatch(targetCount int) (utils.PingDelaySet, bool) {
	if p.position >= len(p.ips) {
		return nil, false
	}

	end := p.position + targetCount
	if end > len(p.ips) {
		end = len(p.ips)
	}

	currentBatch := p.ips[p.position:end]
	p.csv = make(utils.PingDelaySet, 0) // 清空之前的结果

	if len(currentBatch) == 0 {
		return nil, false
	}

	mode := "TCP"
	if Httping {
		mode = "HTTP"
	}
	fmt.Printf("开始延迟测速（模式：%s, 端口：%d, 范围：%v ~ %v ms, 丢包：%.2f)\n",
		mode,
		TCPPort,
		utils.InputMinDelay.Milliseconds(),
		utils.InputMaxDelay.Milliseconds(),
		utils.InputMaxLossRate,
	)

	for _, ip := range currentBatch {
		p.wg.Add(1)
		p.control <- false
		go p.start(ip)
	}
	p.wg.Wait()

	p.position = end // 更新位置
	hasMore := p.position < len(p.ips)

	sort.Sort(p.csv)
	return p.csv, hasMore
}

func (p *Ping) start(ip *net.IPAddr) {
	defer p.wg.Done()
	p.tcpingHandler(ip)
	<-p.control
}

// bool connectionSucceed float32 time
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

// pingReceived pingTotalTime
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

func (p *Ping) appendIPData(data *utils.PingData) {
	p.m.Lock()
	defer p.m.Unlock()
	p.csv = append(p.csv, utils.CloudflareIPData{
		PingData: data,
	})
}

// handle tcping
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
	data := &utils.PingData{
		IP:       ip,
		Sended:   PingTimes,
		Received: recv,
		Delay:    totalDelay / time.Duration(recv),
	}
	p.appendIPData(data)
}
