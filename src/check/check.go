package check

import (
	"fmt"
	"github.com/go-playground/validator/v10"
	"log"
	"os"
	"regexp"
	"searchlog/handle"
	"strconv"
	"strings"
)

var HostName string

func init() {
	HostName, _ = os.Hostname()
}

func checkHostName(fl validator.FieldLevel) bool {
	return fl.Field().String() == HostName
}

func checkIsInt(fl validator.FieldLevel) bool {
	defer func() {
		if err := recover(); err != nil {
		}
	}()
	ff := fl.Field().Float()
	if ff == float64(int64(ff)) {
		return true
	} else {
		return false
	}
}

func checkIsStr(fl validator.FieldLevel) bool {
	return !checkIsInt(fl)
}

func checkIsBool(fl validator.FieldLevel) bool {
	defer func() {
		if err := recover(); err != nil {
			log.Println(err)
		}
	}()
	fl.Field().Bool()
	return true
}

func checkDatePosition(fl validator.FieldLevel) bool {
	defer func() {
		if err := recover(); err != nil {
			log.Println(err)
		}
	}()
	str := fl.Field().String()
	strList := strings.Split(str, ",")
	if len(strList) == 0 {
		return false
	}
	for _, v := range strList {
		i, err := strconv.Atoi(v)
		if err != nil {
			return false
		}
		if i > 100 {
			return false
		}
	}
	return true
}

func checkLogPathName(paths string, name string, p *[]string) bool {
	pathList := strings.Split(paths, "|")
	var fileList []string
	for _, path := range pathList {
		fileList, _ = handle.GetAllFile(path, fileList)
	}
	if len(fileList) == 0 {
		return false
	}
	var logList []string
	for _, file := range fileList {
		fileNameList := strings.Split(file, "/")
		fileName := fileNameList[len(fileNameList)-1]
		matched, err := regexp.MatchString(name, fileName)
		if err != nil {
			log.Println("LogName match error:", err)
			return false
		}
		if matched {
			logList = append(logList, file)
		}
	}
	if len(logList) == 0 {
		return false
	}
	*p = logList
	return true
}

func checkSelectRegular(data map[string]interface{}) (string, bool) {
	rules := map[string]interface{}{
		"colNum": "checkIsInt,gte=0,lt=256",
		"value":  "required,checkIsStr,max=1024",
		"way":    "checkIsInt,gte=0,lte=2",
	}

	validate := validator.New()
	_ = validate.RegisterValidation("checkIsInt", checkIsInt)
	_ = validate.RegisterValidation("checkIsStr", checkIsStr)
	validateMap := validate.ValidateMap(data, rules)
	for k, v := range validateMap {
		msg := fmt.Sprintf("Error parameter selectRegular's list: %s (%s).", k, fmt.Sprint(v))
		return msg, false
	}
	return "", true
}

func ScriptCheck(data map[string]interface{}, ScriptPath string) (string, bool) {
	hostName, ok := data["hostName"]
	if !ok {
		return "Not found hostName!", false
	}
	if fmt.Sprint(hostName) != HostName {
		return "Not match hostName!", false
	}
	scriptName, ok := data["scriptName"]
	if !ok {
		return "Not found scriptName!", false
	}
	var scriptFiles []string
	scriptFiles, _ = handle.GetAllDir(ScriptPath, scriptFiles)
	if !handle.InSlice(scriptFiles, fmt.Sprint(scriptName)) {
		return "Not allowed scriptName!", false
	}
	args, ok := data["args"]
	if ok {
		for _, val := range args.([]interface{}) {
			if arg, ok := val.(string); !ok {
				return "Parameter args list value type is not string!", false
			} else {
				if strings.Contains(arg, "'") || strings.Contains(arg, "\"") || strings.Contains(arg, "\\") {
					return "Arg is not allowed(' \" \\)!", false
				}
			}
		}
	}
	return "", true
}

func FreeSearchCheck(data map[string]interface{}, p *[]string) (string, bool) {
	rules := map[string]interface{}{
		"hostName":      "required,checkHostName",
		"startTime":     "required,checkIsInt,gt=0,lt=9000000000",
		"endTime":       "required,checkIsInt,gt=0",
		"taskId":        "required,min=2,max=64,alphanum,lowercase",
		"logType":       "required,min=2,max=20,ascii,lowercase,excludesall=#*:;? <>/0x2C_0x7C",
		"logPath":       "required,min=2",
		"logName":       "required,min=1,max=255",
		"delimiter":     "required,min=1,max=10",
		"datePosition":  "required,min=1,max=10,checkDatePosition",
		"dateFormat":    "required,min=2,max=64",
		"maxCount":      "omitempty,checkIsInt,gt=0,lte=1000000",
		"selectRegular": "omitempty",
		"deAllInOne":    "omitempty,checkIsBool",
		"logHeader":     "omitempty",
	}

	validate := validator.New()
	_ = validate.RegisterValidation("checkHostName", checkHostName)
	_ = validate.RegisterValidation("checkIsInt", checkIsInt)
	_ = validate.RegisterValidation("checkDatePosition", checkDatePosition)
	_ = validate.RegisterValidation("checkIsBool", checkIsBool)

	validateMap := validate.ValidateMap(data, rules)
	for k, v := range validateMap {
		msg := fmt.Sprintf("Error parameter %s (%s).", k, fmt.Sprint(v))
		return msg, false
	}
	sT := int(data["startTime"].(float64))
	eT := int(data["endTime"].(float64))
	if eT < sT {
		return "Error parameter endTime,info: endTime>=startTime", false
	}
	// 允许的查询时间范围：24小时
	if eT-sT > 3600*24 {
		return "Error parameter endTime,info: endTime-startTime>1day", false
	}
	lP := fmt.Sprint(data["logPath"])
	lN := fmt.Sprint(data["logName"])
	if !checkLogPathName(lP, lN, p) {
		return "Error parameter logPath or logName,info: path or file not matched!", false
	}
	selectRegular, ok := data["selectRegular"]
	if ok {
		selectRegularList, ok := selectRegular.([]interface{})
		if !ok {
			return "Error parameter selectRegular,info: value not a list", false
		}
		for _, v := range selectRegularList {
			vMap, ok := v.(map[string]interface{})
			if !ok {
				return "Error parameter selectRegular's list,info: value not a dict", false
			}
			msg, ok := checkSelectRegular(vMap)
			if !ok {
				return msg, ok
			}
		}
	}
	logHeader, ok := data["logHeader"]
	if ok {
		logHeaderList, ok := logHeader.([]interface{})
		if !ok {
			return "Error parameter logHeader,info: value not a list", false
		}
		for _, v := range logHeaderList {
			val := strings.TrimSpace(fmt.Sprint(v))
			if len(val) == 0 || len(val) > 20 {
				return "Error parameter logHeader,info: value length must between 1 and 20", false
			}
		}
	}
	return "", true
}
