package task

import (
	"bufio"
	"log"
	"math"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultInputFile = "ip.txt"

var (
	// TestAll4 test all ip
	TestAll4 = false
	// IPFile is the filename of IP Rangs
	IPFile = defaultInputFile
	IPText string
	// More6 -more6 parameter
	More6 = false
	// Lots6 -lots6 parameter
	Lots6 = false
	// Many6 -many6 parameter
	Many6 = false
	// Some6 -some6 parameter
	Some6 = false
	// Many4 -many4 parameter
	Many4 = false
	// V4Param -v4 parameter
	V4Param = ""
	// V6Param -v6 parameter
	V6Param = ""
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

// 解析类似 "8-5" 或 "9" 这样的参数
func parseNumParam(param string) (int64, error) {
	if param == "" {
		return 0, nil
	}
	
	var base, offset int64
	var err error
	
	// 检查是否包含加减符号
	if idx := strings.IndexAny(param, "+-"); idx != -1 {
		base, err = strconv.ParseInt(param[:idx], 10, 64)
		if err != nil {
			return 0, err
		}
		offset, err = strconv.ParseInt(param[idx:], 10, 64)
		if err != nil {
			return 0, err
		}
		return int64(math.Pow(2, float64(base))) + offset, nil
	}
	
	// 单个数字的情况
	base, err = strconv.ParseInt(param, 10, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Pow(2, float64(base))), nil
}

// 获取应该测试的 IP 数量，仅当指定了相应参数时才返回具体数量，否则返回 0 表示使用默认随机逻辑
func getTestCount(isIPv4 bool) int64 {
	if isIPv4 {
		// 处理 IPv4 的情况
		if TestAll4 {
			return math.MaxInt64
		}
		if Many4 {
			v4Count, _ := parseNumParam("12")
			if V4Param != "" {
				if count, err := parseNumParam(V4Param); err == nil && count < v4Count {
					return count
				}
			}
			return v4Count
		}
		if V4Param != "" {
			if count, err := parseNumParam(V4Param); err == nil {
				if count >= 0 && count <= int64(math.Pow(2, 16)) {
					return count
				}
			}
		}
		return 0 // 返回 0 表示使用默认随机逻辑
	} else {
		// 处理 IPv6 的情况
		if More6 {
			v6Count, _ := parseNumParam("18")
			if V6Param != "" {
				if count, err := parseNumParam(V6Param); err == nil && count < v6Count {
					return count
				}
			}
			return v6Count
		}
		if Lots6 {
			v6Count, _ := parseNumParam("16")
			if V6Param != "" {
				if count, err := parseNumParam(V6Param); err == nil && count < v6Count {
					return count
				}
			}
			return v6Count
		}
		if Many6 {
			v6Count, _ := parseNumParam("12")
			if V6Param != "" {
				if count, err := parseNumParam(V6Param); err == nil && count < v6Count {
					return count
				}
			}
			return v6Count
		}
		if Some6 {
			v6Count, _ := parseNumParam("8")
			if V6Param != "" {
				if count, err := parseNumParam(V6Param); err == nil && count < v6Count {
					return count
				}
			}
			return v6Count
		}
		if V6Param != "" {
			if count, err := parseNumParam(V6Param); err == nil {
				if count >= 0 && count <= int64(math.Pow(2, 96)) {
					return count
				}
			}
		}
		return 0 // 返回 0 表示使用默认随机逻辑
	}
}

func (r *IPRanges) chooseIPv4() {
	if r.mask == "/32" { // 单个 IP 则无需随机，直接加入自身即可
		r.appendIP(r.firstIP)
		return
	}
	
	minIP, hosts := r.getIPRange()
	targetCount := getTestCount(true)
	
	if targetCount > 0 {
		// 使用指定的数量
		if targetCount >= int64(hosts)+1 {
			// 如果目标数量大于等于可用IP数量，测试所有IP
			for i := 0; i <= int(hosts); i++ {
				r.appendIPv4(byte(i) + minIP)
			}
			return
		}
		
		// 随机选择不重复的IP
		used := make(map[byte]bool)
		for int64(len(used)) < targetCount {
			ip := minIP + randIPEndWith(hosts)
			if !used[ip] {
				used[ip] = true
				r.appendIPv4(ip)
			}
		}
	} else {
		// 使用原有的随机逻辑
		for r.ipNet.Contains(r.firstIP) {
			r.appendIPv4(minIP + randIPEndWith(hosts))
			r.firstIP[14]++
			if r.firstIP[14] == 0 {
				r.firstIP[13]++ // 0.(X+1).X.X
				if r.firstIP[13] == 0 {
					r.firstIP[12]++ // (X+1).X.X.X
				}
			}
		}
	}
}

func (r *IPRanges) chooseIPv6() {
	if r.mask == "/128" { // 单个 IP 则无需随机，直接加入自身即可
		r.appendIP(r.firstIP)
		return
	}
	
	targetCount := getTestCount(false)
	
	if targetCount > 0 {
		// 使用指定的数量
		used := make(map[string]bool)
		for int64(len(used)) < targetCount {
			newIP := make([]byte, len(r.firstIP))
			copy(newIP, r.firstIP)
			
			for i := len(newIP)-1; i >= 0; i-- {
				newIP[i] = byte(rand.Intn(256))
				if r.ipNet.Contains(newIP) {
					ipStr := net.IP(newIP).String()
					if !used[ipStr] {
						used[ipStr] = true
						r.appendIP(newIP)
						break
					}
				}
			}
		}
	} else {
		var tempIP uint8                  // 临时变量，用于记录前一位的值
		for r.ipNet.Contains(r.firstIP) { // 只要该 IP 没有超出 IP 网段范围，就继续循环随机
			r.firstIP[15] = randIPEndWith(255) // 随机 IP 的最后一段
			r.firstIP[14] = randIPEndWith(255) // 随机 IP 的最后一段

			targetIP := make([]byte, len(r.firstIP))
			copy(targetIP, r.firstIP)
			r.appendIP(targetIP) // 加入 IP 地址池

			for i := 13; i >= 0; i-- { // 从倒数第三位开始往前随机
				tempIP = r.firstIP[i]              // 保存前一位的值
				r.firstIP[i] += randIPEndWith(255) // 随机 0~255，加到当前位上
				if r.firstIP[i] >= tempIP {        // 如果当前位的值大于等于前一位的值，说明随机成功了，可以退出该循环
					break
				}
			}
		}
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
	return ranges.ips
}
