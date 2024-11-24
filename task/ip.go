package task

import (
	"bufio"
	"log"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"math"
	"regexp"
)

const (
	defaultInputFile = "ip.txt"
	maxIPv4Power    = 16  // 2^16 = 65536
	maxIPv6Power    = 20  // 2^20 = 1048576
	maxTestQueue    = 200000  // 最大延迟测速队列
)

var (
	// TestAll4 test all ip
	TestAll4 = false
	// IPv4TestNum 指定 IPv4 测试数量
	IPv4TestNum = 0
	// IPv6TestNum 指定 IPv6 测试数量
	IPv6TestNum = 0
	// IPFile is the filename of IP Rangs
	IPFile = defaultInputFile
	IPText string
)

func InitRandSeed() {
	rand.Seed(time.Now().UnixNano())
}

func isIPv4(ip string) bool {
	return strings.Contains(ip, ".")
}

func randIPEndWith(num byte) byte {
	if num == 0 { // 对于 /32 这种单独的 IP
		return byte(0)
	}
	return byte(rand.Intn(int(num)))
}

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

// 如果是单独 IP 则加上子网掩码，反之则获取子网掩码(r.mask)
func (r *IPRanges) fixIP(ip string) string {
	// 如果不含有 '/' 则代表不是 IP 段，而是一个单独的 IP，因此需要加上 /32 /128 子网掩码
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

// 解析 IP 段，获得 IP、IP 范围、子网掩码
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

// 返回第四段 ip 的最小值及可用数目
func (r *IPRanges) getIPRange() (minIP, hosts byte) {
	minIP = r.firstIP[15] & r.ipNet.Mask[3] // IP 第四段最小值

	// 根据子网掩码获取主机数量
	m := net.IPv4Mask(255, 255, 255, 255)
	for i, v := range r.ipNet.Mask {
		m[i] ^= v
	}
	total, _ := strconv.ParseInt(m.String(), 16, 32) // 总可用 IP 数
	if total > 255 {                                 // 矫正 第四段 可用 IP 数
		hosts = 255
		return
	}
	hosts = byte(total)
	return
}

// parseTestNum 解析并限制测试数量参数 (2^n±m)
func ParseTestNum(param string, isIPv4 bool) int {
	if param == "" {
		return 0
	}
	
	maxPower := maxIPv6Power
	if isIPv4 {
		maxPower = maxIPv4Power
	}
	
	re := regexp.MustCompile(`^(\d+)([\+\-])(\d+)$`)
	matches := re.FindStringSubmatch(param)
	
	var num int
	if matches == nil {
		// 如果不是 n±m 格式，则视为普通数字
		num, _ = strconv.Atoi(param)
	} else {
		n, _ := strconv.Atoi(matches[1])
		m, _ := strconv.Atoi(matches[3])
		
		if n > maxPower { // 限制最大幂
			n = maxPower
		}
		
		base := int(math.Pow(2, float64(n)))
		if matches[2] == "+" {
			num = base + m
		} else {
			num = base - m
		}
	}
	
	// 确保不超过最大值
	maxNum := int(math.Pow(2, float64(maxPower)))
	if num > maxNum {
		num = maxNum
	}
	return num
}

// 计算 CIDR 中可用的 IP 数量
func calculateCIDRSize(ipNet *net.IPNet) int {
	ones, bits := ipNet.Mask.Size()
	return 1 << uint(bits-ones)
}

// 从较大的数组中快速随机选择指定数量的元素
func fastRandomSelect(ips []*net.IPAddr, count int) []*net.IPAddr {
	n := len(ips)
	if n <= count {
		return ips
	}
	
	// 创建索引数组
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}
	
	// Fisher-Yates 洗牌算法的简化版本
	selected := make([]*net.IPAddr, count)
	for i := 0; i < count; i++ {
		j := rand.Intn(n - i)
		selected[i] = ips[indices[j]]
		indices[j] = indices[n-i-1]
	}
	
	return selected
}

func (r *IPRanges) chooseIPv4() {
	if r.mask == "/32" { // 单个 IP 则无需随机，直接加入自身即可
		r.appendIP(r.firstIP)
		return
	}
	
	minIP, hosts := r.getIPRange()
	cidrSize := int(hosts) + 1
	testNum := IPv4TestNum
	
	if testNum > cidrSize {
		testNum = cidrSize
	}
	
	for r.ipNet.Contains(r.firstIP) {
		if TestAll4 || testNum >= cidrSize {
			// 测试所有 IP
			for i := 0; i <= int(hosts); i++ {
				r.appendIPv4(byte(i) + minIP)
			}
		} else if testNum > 0 {
			// 生成不重复的随机数
			nums := make(map[int]bool)
			for len(nums) < testNum {
				num := rand.Intn(cidrSize)
				if !nums[num] {
					nums[num] = true
					r.appendIPv4(byte(num) + minIP)
				}
			}
		} else { // 默认随机一个
			r.appendIPv4(minIP + randIPEndWith(hosts))
		}
		
		r.firstIP[14]++
		if r.firstIP[14] == 0 {
			r.firstIP[13]++
			if r.firstIP[13] == 0 {
				r.firstIP[12]++
			}
		}
	}
}

func (r *IPRanges) chooseIPv6() {
	if r.mask == "/128" {
		r.appendIP(r.firstIP)
		return
	}
	
	cidrSize := calculateCIDRSize(r.ipNet)
	testNum := IPv6TestNum
	
	if testNum <= 0 {
		// 默认只测一个
		r.appendRandomIPv6()
		return
	}
	
	if testNum > cidrSize {
		testNum = cidrSize
	}
	
	// 使用 map 确保生成的 IPv6 不重复
	ipMap := make(map[string]bool)
	for len(ipMap) < testNum {
		ip := r.generateRandomIPv6()
		if ip != nil && !ipMap[string(ip)] {
			ipMap[string(ip)] = true
			r.appendIP(ip)
		}
	}
}

// generateRandomIPv6 生成一个随机的 IPv6 地址
func (r *IPRanges) generateRandomIPv6() net.IP {
	targetIP := make([]byte, len(r.firstIP))
	copy(targetIP, r.firstIP)
	
	// 随机化后8个字节
	for i := 8; i < 16; i++ {
		targetIP[i] = byte(rand.Intn(256))
	}
	
	if r.ipNet.Contains(targetIP) {
		return targetIP
	}
	return nil
}

// appendRandomIPv6 已不再使用，保留是为了向后兼容
func (r *IPRanges) appendRandomIPv6() {
	if ip := r.generateRandomIPv6(); ip != nil {
		r.appendIP(ip)
	}
}

func loadIPRanges() []*net.IPAddr {
	ranges := newIPRanges()
	if IPText != "" { // 从参数中获取 IP 段数据
			IPs := strings.Split(IPText, ",") // 以逗号分隔为数组并循环遍历
			for _, IP := range IPs {
				IP = strings.TrimSpace(IP) // 去除首尾的空白字符（空格、制表符、换行符等）
				if IP == "" {              // 跳过空的（即开头、结尾或连续多个 ,, 的情况）
					continue
				}
				ranges.parseCIDR(IP) // 解析 IP 段，获得 IP、IP 范围、子网掩码
				if isIPv4(IP) {      // 生成要测速的所有 IPv4 / IPv6 地址（单个/随机/全部）
					ranges.chooseIPv4()
				} else {
					ranges.chooseIPv6()
				}
			}
	} else { // 从文件中获取 IP 段数据
		if IPFile == "" {
			IPFile = defaultInputFile
		}
		file, err := os.Open(IPFile)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() { // 循环遍历文件每一行
			line := strings.TrimSpace(scanner.Text()) // 去除首尾的空白字符（空格、制表符、换行符等）
			if line == "" {                           // 跳过空行
				continue
			}
			ranges.parseCIDR(line) // 解析 IP 段，获得 IP、IP 范围、子网掩码
			if isIPv4(line) {      // 生成要测速的所有 IPv4 / IPv6 地址（单个/随机/全部）
				ranges.chooseIPv4()
			} else {
				ranges.chooseIPv6()
			}
		}
	}
	
	// 如果 IP 总数超过最大测试队列，随机选择部分 IP
	if len(ranges.ips) > maxTestQueue {
		ranges.ips = fastRandomSelect(ranges.ips, maxTestQueue)
	}
	
	return ranges.ips
}
