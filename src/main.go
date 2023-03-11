package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"github.com/olivere/elastic"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"searchlog/check"
	"searchlog/handle"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"context"
	"github.com/Unknwon/goconfig"
	"github.com/gin-gonic/gin"
	"github.com/unrolled/secure"
)

var AllowList []string
var ListenIp string
var ListenPort int
var MaxCount int
var RetUrl string
var ScriptPath string
var EsHost string
var EsUser string
var EsPass string
var EsCh = make(chan bool, 5)
var esClient *elastic.Client

type RuleStruct struct {
	Value  string
	Way    int
	ColNum int
}

// 读取配置文件参数，全局变量初始化，连接ES
func init() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	config, err := goconfig.LoadConfigFile("config/config.ini")
	if err != nil {
		log.Fatalf("无法加载配置文件：%s", err)
	}
	AllowList = strings.Split(config.MustValue("All", "allowIps"), ",")
	ListenIp = config.MustValue("All", "ListenIp")
	ListenPort, _ = strconv.Atoi(config.MustValue("All", "listenPort"))
	MaxCount, _ = strconv.Atoi(config.MustValue("LogSearch", "maxCount"))
	RetUrl = config.MustValue("LogSearch", "retUrl")
	ScriptPath = config.MustValue("RunScript", "scriptPath")
	EsHost = config.MustValue("LogSearch", "esHost")
	EsUser = config.MustValue("LogSearch", "esUser")
	EsPass = config.MustValue("LogSearch", "esPass")

	esClient, err = elastic.NewClient(elastic.SetURL(EsHost), elastic.SetBasicAuth(EsUser, EsPass), elastic.SetSniff(false))
	if err != nil {
		panic(err)
	}
	info, code, err := esClient.Ping(EsHost).Do(context.Background())
	if err != nil {
		panic(err)
	}
	log.Printf("Es return with code %d and version %s \n", code, info.Version.Number)
}

// 调用GinHttps函数，启动HTTPS server
func main() {
	runtime.GOMAXPROCS(1)
	err := GinHttps(true)
	if err != nil {
		return
	}
}

// 执行运行指定脚本的命令，实现/agent/run/script接口功能
func runScript(data map[string]interface{}) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
	defer cancel()
	var argList []string
	for _, val := range data["args"].([]interface{}) {
		argList = append(argList, val.(string))
	}
	cmdStr := "cd " + ScriptPath + "/" + fmt.Sprint(data["scriptName"]) + ";sh run.sh " + strings.Join(argList, " ")
	log.Println(cmdStr)
	runCmd := exec.CommandContext(ctx, "/bin/bash", "-c", cmdStr)
	runCmdOut, err := runCmd.CombinedOutput()
	if err != nil {
		return "命令执行失败：" + fmt.Sprintf("%s", err)
	} else {
		return string(runCmdOut)
	}
}

func getGzFile(gzDict map[string][2]int64, startTime int64, endTime int64) []string {
	var retList []string
	for k, val := range gzDict {
		if val[0] <= endTime && val[1] >= startTime {
			retList = append(retList, k)
		}
	}
	return retList
}

// 判断这个文件的[首行/尾行]时间是否符合条件
func isTimeOk(strList []string, ts int64, datePosition []int, dateFormat string, _type bool) (int64, bool) {
	defer func() {
		if err := recover(); err != nil {
		}
	}()
	var dateStr string
	dpLen := len(datePosition)
	if dpLen < 2 {
		dateStr = strList[datePosition[0]]
	} else if dpLen == 2 {
		dateStr = strList[datePosition[0]] + " " + strList[datePosition[1]]
	} else {
		dateStr = strList[datePosition[0]] + " " + strList[datePosition[1]] + " " + strList[datePosition[2]]
	}
	stamp, _ := time.ParseInLocation(dateFormat, dateStr, time.Local)

	uTs := handle.FillYear(stamp)
	if _type {
		// 文件头的时间与查询结束时间比较，符合时返回 文件头的时间戳、true
		if uTs < ts+1 {
			return uTs, true
		}
	} else {
		// 文件尾的时间与查询开始时间比较，符合时返回 文件尾的时间戳、true
		if uTs > ts-1 {
			return uTs, true
		}
	}
	return 0, false
}

// 把文件一行的字符串按指定分割规则，转成切片类型
func strSplit(line string, delimiter string, deAllInOne bool) []string {
	strList := strings.Split(line, delimiter)
	var strListNew []string
	if deAllInOne {
		for _, str := range strList {
			if str != "" {
				strListNew = append(strListNew, str)
			}
		}
	} else {
		strListNew = strList
	}
	return strListNew
}

// 筛选文件，把压缩文件分离出，对非压缩文件进行处理，符合条件的行上传ES
func doFile(fileName string, startTime int64, endTime int64, taskId string, esIndex string, delimiter string,
	datePosition []int, dateFormat string, maxCount int, selectRegularList []RuleStruct, deAllInOne bool,
	logHeader []string, count *int, failCount *int32, gzDict *map[string]int64) {
	file, err := os.Open(fileName)
	if err != nil {
		return
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
		}
	}(file)
	buf := make([]byte, 4096)
	if strings.HasSuffix(fileName, ".gz") {
		gr, err := gzip.NewReader(file)
		if err != nil {
			return
		}
		if strings.HasSuffix(fileName, ".tar.gz") {
			buf := make([]byte, 512)
			_, err = gr.Read(buf)
			if err != nil {
				return
			}
		}
		n, err := gr.Read(buf)
		if err != nil && err != io.EOF {
			return
		}
		lineList := strings.Split(string(buf[:n]), "\n")
		line1 := lineList[0]
		strList := strSplit(line1, delimiter, deAllInOne)
		ts, ok := isTimeOk(strList, endTime, datePosition, dateFormat, true)
		if !ok {
			if len(lineList) > 1 {
				line2 := lineList[1]
				strList := strSplit(line2, delimiter, deAllInOne)
				ts, ok = isTimeOk(strList, endTime, datePosition, dateFormat, true)
				if !ok {
					return
				}
			} else {
				return
			}
		}
		err = gr.Close()
		if err != nil {
			panic(err)
		}
		(*gzDict)[fileName] = ts
	} else {
		// 非压缩文件
		n, err := file.Read(buf)
		if err != nil && err != io.EOF {
			return
		}
		lineList := strings.Split(string(buf[:n]), "\n")
		line1 := lineList[0]
		strList := strSplit(line1, delimiter, deAllInOne)
		_, ok := isTimeOk(strList, endTime, datePosition, dateFormat, true)
		if !ok {
			if len(lineList) > 1 {
				line2 := lineList[1]
				strList := strSplit(line2, delimiter, deAllInOne)
				isTimeOk(strList, endTime, datePosition, dateFormat, true)
				if !ok {
					return
				}
			} else {
				return
			}
		}

		_, err = file.Seek(-4096, 2)
		if err == nil {
			n, err = file.Read(buf)
			if err != nil && err != io.EOF {
				return
			}
			lineList = strings.Split(string(buf[:n]), "\n")
		} else {
			log.Println(err)
		}

		line1 = lineList[len(lineList)-1]
		strList = strSplit(line1, delimiter, deAllInOne)
		_, ok = isTimeOk(strList, startTime, datePosition, dateFormat, false)
		if !ok {
			if len(lineList) > 1 {
				line2 := lineList[len(lineList)-2]
				strList = strSplit(line2, delimiter, deAllInOne)
				_, ok = isTimeOk(strList, startTime, datePosition, dateFormat, false)
				if !ok {
					return
				}
			} else {
				return
			}
		}
		_, err = file.Seek(0, 0)
		if err != nil {
			return
		}
		log.Println(file)
		fr := bufio.NewReader(file)
		cacheString := ""
		for {
			n, err := fr.Read(buf)
			if err != nil && err != io.EOF {
				return
			}
			if n == 0 {
				break
			}
			lineList := strings.Split(string(buf[:n]), "\n")
			lineList[0] = cacheString + lineList[0]
			cacheString = lineList[len(lineList)-1]
			if len(cacheString) > 4096 {
				cacheString = cacheString[:4096] + "......"
			}
			for _, line := range lineList {
				strList := strSplit(line, delimiter, deAllInOne)
				logTs, ok := isTimeTrueLog(strList, startTime, endTime, datePosition, dateFormat)
				if ok && isTrueLog(strList, selectRegularList) {
					go inputES(strList, taskId, esIndex, logHeader, logTs, delimiter, failCount)
					*count += 1
					if *count > maxCount {
						return
					}
				}
			}
		}
	}
}

// 判断这条日志的时间是否符合条件
func isTimeTrueLog(strList []string, sTs int64, eTs int64, datePosition []int, dateFormat string) (int64, bool) {
	defer func() {
		if err := recover(); err != nil {
		}
	}()
	var dateStr string
	dpLen := len(datePosition)
	if dpLen < 2 {
		dateStr = strList[datePosition[0]]
	} else if dpLen == 2 {
		dateStr = strList[datePosition[0]] + " " + strList[datePosition[1]]
	} else {
		dateStr = strList[datePosition[0]] + " " + strList[datePosition[1]] + " " + strList[datePosition[2]]
	}
	stamp, _ := time.ParseInLocation(dateFormat, dateStr, time.Local)

	ts := handle.FillYear(stamp)
	if sTs <= ts && ts <= eTs {
		return ts, true
	} else {
		return ts, false
	}
}

// 精确匹配
func way0(line *[]string, rule *RuleStruct) bool {
	if rule.ColNum == 0 {
		if handle.InSlice(*line, rule.Value) {
			return true
		}
	} else if rule.Value == (*line)[rule.ColNum-1] {
		return true
	}
	return false
}

// 模糊匹配
func way1(line *[]string, rule *RuleStruct) bool {
	if rule.ColNum == 0 {
		for _, li := range *line {
			if strings.Contains(li, rule.Value) {
				return true
			}
		}
	} else if strings.Contains((*line)[rule.ColNum-1], rule.Value) {
		return true
	}
	return false
}

// 正则匹配
func way2(line *[]string, rule *RuleStruct) bool {
	if rule.ColNum == 0 {
		for _, li := range *line {
			if m, _ := regexp.MatchString(rule.Value, li); m {
				return true
			}
		}
	} else if m, _ := regexp.MatchString(rule.Value, (*line)[rule.ColNum-1]); m {
		return true
	}
	return false
}

// 判断这条日志的检索内容是否符合条件，区分规则类型并交给way[0-2]函数判断
func isTrueLog(line []string, rules []RuleStruct) bool {
	for _, rule := range rules {
		if len(line) < rule.ColNum {
			continue
		}
		switch rule.Way {
		case 0:
			if !way0(&line, &rule) {
				return false
			}
		case 1:
			if !way1(&line, &rule) {
				return false
			}
		case 2:
			if !way2(&line, &rule) {
				return false
			}
		}
	}
	return true
}

// 上传数据到ES，通过channel限制最多并发5个协程
func inputES(strList []string, taskId string, esIndex string, logHeader []string, ts int64, delimiter string, failCount *int32) {
	strDict := map[string]string{}
	strDict["_time"] = strconv.FormatInt(ts, 10)
	strDict["_hostname"] = check.HostName
	strDict["0,taskId"] = taskId
	strDict["&,undefined"] = ""
	logHeaderLen := len(logHeader)
	for i, str := range strList {
		if i < logHeaderLen {
			strDict[logHeader[i]] = str
		} else {
			strDict["&,undefined"] += str + delimiter
			// strDict[strconv.Itoa(i+1)] = str
		}
	}
	strDict["&,undefined"] = strings.TrimRight(strDict["&,undefined"], delimiter)
	marshal, _ := json.Marshal(strDict)
	EsCh <- true
	defer func() {
		<-EsCh
	}()
	_, err := esClient.Index().Index(esIndex).Type("_doc").BodyString(string(marshal)).Do(context.Background())
	if err != nil {
		atomic.AddInt32(failCount, 1)
	}
}

// 顺序读取与解压gz压缩文件，缓存区4kb（过长的行会被截断），把符合条件的行上传到ES
func doGzFile(fileName string, startTime int64, endTime int64, taskId string, esIndex string, delimiter string,
	datePosition []int, dateFormat string, maxCount int, selectRegularList []RuleStruct, deAllInOne bool,
	logHeader []string, count *int, failCount *int32) {
	file, _ := os.Open(fileName)
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
		}
	}(file)
	gr, err := gzip.NewReader(file)
	if err != nil {
		return
	}
	buf := make([]byte, 4096)
	cacheString := ""
	for {
		n, err := gr.Read(buf)
		if err != nil && err != io.EOF {
			return
		}
		if n == 0 {
			break
		}
		lineList := strings.Split(string(buf[:n]), "\n")
		lineList[0] = cacheString + lineList[0]
		cacheString = lineList[len(lineList)-1]
		if len(cacheString) > 4096 {
			cacheString = cacheString[:4096] + "......"
		}
		for _, line := range lineList {
			strList := strSplit(line, delimiter, deAllInOne)
			logTs, ok := isTimeTrueLog(strList, startTime, endTime, datePosition, dateFormat)
			if ok && isTrueLog(strList, selectRegularList) {
				go inputES(strList, taskId, esIndex, logHeader, logTs, delimiter, failCount)
				*count += 1
				if *count > maxCount {
					return
				}
			}
		}
	}
	err = gr.Close()
	if err != nil {
		return
	}
}

// 按文件内容的时间顺序排序
func sortFiles(gzDict map[string]int64) map[string][2]int64 {
	var retDict = map[string][2]int64{}
	var tmpDict = map[int64]string{}
	var tmpList []int64
	for k, v := range gzDict {
		tmpDict[v] = k
	}
	for k := range tmpDict {
		tmpList = append(tmpList, k)
	}
	sort.Slice(tmpList, func(i, j int) bool {
		return tmpList[i] < tmpList[j]
	})
	var nextTs int64
	for i, ts := range tmpList {
		if i < len(tmpList)-1 {
			nextTs = tmpList[i+1]
			retDict[tmpDict[ts]] = [2]int64{ts, nextTs}
		} else {
			retDict[tmpDict[ts]] = [2]int64{ts, 9000000000}
		}
	}
	return retDict
}

// 日志检索，参数获取与初始化、doFile和doGzFile分别用来打开未压缩文件和压缩文件对文件内容做详细筛选
func runFreeSearch(data map[string]interface{}, fileList []string) {
	startTime := int64(data["startTime"].(float64))
	endTime := int64(data["endTime"].(float64))
	taskId := fmt.Sprint(data["taskId"])
	logType := fmt.Sprint(data["logType"])
	delimiter := fmt.Sprint(data["delimiter"])
	datePosition := fmt.Sprint(data["datePosition"])
	datePositionList := strings.Split(datePosition, ",")
	selectRegular, ok := data["selectRegular"]

	var selectRegularList []RuleStruct
	if ok {
		tmpList, _ := selectRegular.([]interface{})
		for _, v := range tmpList {
			ru := RuleStruct{}
			vMap := v.(map[string]interface{})
			arr, _ := json.Marshal(vMap)
			err := json.Unmarshal(arr, &ru)
			if err != nil {
				return
			}
			selectRegularList = append(selectRegularList, ru)
		}
	}

	logHeader, ok := data["logHeader"]
	var logHeaderList []string
	if ok {
		tmpList, _ := logHeader.([]interface{})
		for _, v := range tmpList {
			logHeaderList = append(logHeaderList, fmt.Sprint(v))
		}
	}

	var datePositionRet []int
	for _, v := range datePositionList {
		vi, _ := strconv.Atoi(v)
		datePositionRet = append(datePositionRet, vi-1)
	}
	dateFormat := fmt.Sprint(data["dateFormat"])
	var maxCount int
	_, ok = data["maxCount"]
	if ok {
		maxCount = int(data["maxCount"].(float64))
	} else {
		maxCount = MaxCount
	}
	var deAllInOne bool
	_, ok = data["deAllInOne"]
	if ok {
		deAllInOne = data["deAllInOne"].(bool)
	} else {
		deAllInOne = false
	}
	esIndex := "log_search_" + logType + "_" + time.Unix(time.Now().Unix(), 0).Format("20060102")
	exist, _ := esClient.IndexExists(esIndex).Do(context.Background())
	if !exist {
		headerMap := map[string]map[string]string{}
		headerMap["_time"] = map[string]string{"type": "date", "format": "epoch_second"}
		headerMap["_hostname"] = map[string]string{"type": "keyword"}
		headerMap["0,taskId"] = map[string]string{"type": "keyword"}
		headerMap["*"] = map[string]string{"type": "keyword"}
		for _, v := range logHeaderList {
			headerMap[v] = map[string]string{"type": "keyword"}
		}
		marshal, _ := json.Marshal(headerMap)
		// 将不确定的字段通过动态模板方式都定义类型为keyword，防止日期型的字段被ES自动  定义为date类型
		mapping := `{"settings": {"index": {"max_result_window": "1000000000"}}, "mappings": { "dynamic_templates": [
{"string_fields": {"match": "*", "match_mapping_type": "string", "mapping": {"type": "keyword"}}},
{"date_fields": {"match": "*", "match_mapping_type": "date", "mapping": {"type": "keyword"}}}], "properties": **}}`
		mapping = strings.Replace(mapping, "**", string(marshal), 1)
		createIndex, err := esClient.CreateIndex(esIndex).BodyString(mapping).Do(context.Background())
		if err != nil {
			log.Println(err)
		} else {
			if !createIndex.Acknowledged {
				log.Println("es索引创建失败")
			}
		}
	}

	var gzDict = map[string]int64{}
	var nowCount int
	var failCount int32
	// 先用doFile过滤全部初筛文件，处理符合的未压缩文件，最终把检索到的行上传到ES；把可能符合的压缩文件保存在gzDict中
	for _, file := range fileList {
		doFile(file, startTime, endTime, taskId, esIndex, delimiter, datePositionRet, dateFormat, maxCount,
			selectRegularList, deAllInOne, logHeaderList, &nowCount, &failCount, &gzDict)
	}
	if len(gzDict) != 0 {
		// 对doFile筛选出的gz压缩文件进行再次筛选
		gzFiles := sortFiles(gzDict)
		gzFileList := getGzFile(gzFiles, startTime, endTime)
		log.Println(gzFileList)
		// 处理再次筛选后的压缩文件，最终把检索到的行上传到ES
		for _, gzFile := range gzFileList {
			doGzFile(gzFile, startTime, endTime, taskId, esIndex, delimiter, datePositionRet, dateFormat, maxCount,
				selectRegularList, deAllInOne, logHeaderList, &nowCount, &failCount)
		}
	}

	// 完成后回调接口
	type RetStruct struct {
		TaskId     string
		HostName   string
		DoneTs     int64
		TotalCount int
		FailCount  int32
	}
	retSt := RetStruct{
		TaskId:     taskId,
		HostName:   check.HostName,
		DoneTs:     time.Now().Unix(),
		TotalCount: nowCount,
		FailCount:  failCount,
	}
	jsonBytes, _ := json.Marshal(retSt)
	jsonMsg := string(jsonBytes)
	log.Println(jsonMsg)
	resp, err := http.Post(RetUrl, "text/json;charset=utf-8", strings.NewReader(jsonMsg))
	if err != nil {
		log.Printf("post请求失败 error: %+v", err)
		return
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			return
		}
	}(resp.Body)
}

// /agent/log/freeSearch，运行自定义日志检索任务
func freeSearch(c *gin.Context) {
	jsonMap := make(map[string]interface{})
	err := c.BindJSON(&jsonMap)
	if err != nil {
		c.JSON(400, gin.H{"code": 400, "msg": "Json format error!"})
		return
	}
	var filePathList []string
	msg, ok := check.FreeSearchCheck(jsonMap, &filePathList)
	if !ok {
		c.JSON(400, gin.H{"code": 400, "msg": msg})
		return
	}
	c.JSON(200, gin.H{"code": 200, "msg": "Log search task is running"})
	// 请求参数校验通过后，响应200后，正式开始运行检索任务。协程运行，传入请求参数和在设备上读取到的文件列表(初筛)
	go runFreeSearch(jsonMap, filePathList)
}

// /agent/run/script，运行运维脚本接口
func script(c *gin.Context) {
	jsonMap := make(map[string]interface{})
	err := c.BindJSON(&jsonMap)
	if err != nil {
		c.JSON(400, gin.H{"code": 400, "msg": "Json format error!"})
		return
	}
	msg, ok := check.ScriptCheck(jsonMap, ScriptPath)
	if !ok {
		c.JSON(400, gin.H{"code": 400, "msg": msg})
		return
	}
	ret := runScript(jsonMap)

	c.String(200, "%s", ret)
}

// GinHttps server 启动，IP白名单限制、路由添加
func GinHttps(isHttps bool) error {
	// gin日志输出文件
	f, _ := os.Create("access.log")
	gin.DefaultWriter = io.MultiWriter(f)
	// 设置运行模式：debug，release
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.Use(handle.Recover)
	r.Use(handle.IPWhiteList(AllowList))

	r.GET("/agent/isAvailable", func(c *gin.Context) {
		c.JSON(200, gin.H{"code": 200, "ip": ListenIp, "msg": "success"})
	})

	r.POST("/agent/log/freeSearch", freeSearch)

	r.POST("/agent/run/script", script)

	if isHttps {
		r.Use(TlsHandler(ListenPort))

		return r.RunTLS(ListenIp+":"+strconv.Itoa(ListenPort), "config/server.pem", "config/server.key")
	}

	return r.Run(ListenIp + ":" + strconv.Itoa(ListenPort))
}

// TlsHandler 用于支持SSL，开启HTTPS安全端口
func TlsHandler(port int) gin.HandlerFunc {
	return func(c *gin.Context) {
		secureMiddleware := secure.New(secure.Options{
			SSLRedirect: true,
			SSLHost:     ":" + strconv.Itoa(port),
		})
		err := secureMiddleware.Process(c.Writer, c.Request)

		// If there was an error, do not continue.
		if err != nil {
			return
		}

		c.Next()
	}
}
