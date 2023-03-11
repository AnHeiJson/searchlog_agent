package handle

import (
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"time"
)

// Recover 用于处理Gin中的异常，防止程序panic
func Recover(c *gin.Context) {
	defer func() {
		if r := recover(); r != nil {
			//打印错误堆栈信息
			log.Printf("panic: %v\n", r)
			debug.PrintStack()
			//封装通用json返回
			//c.JSON(http.StatusOK, Result.Fail(errorToString(r)))
			//Result.Fail不是本例的重点，因此用下面代码代替
			c.JSON(http.StatusOK, gin.H{
				"code": "1",
				"msg":  errorToString(r),
				"data": nil,
			})
			//终止后续接口调用，不加的话recover到异常后，还会继续执行接口里后续代码
			c.Abort()
		}
	}()
	//加载完 defer recover，继续后续接口调用
	c.Next()
}

// recover错误，转string
func errorToString(r interface{}) string {
	switch v := r.(type) {
	case error:
		return v.Error()
	default:
		return r.(string)
	}
}

func IPWhiteList(whitelist []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !InSlice(whitelist, c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"status":  http.StatusForbidden,
				"message": "Permission denied",
			})
			return
		}
	}
}

// InSlice 判断字符串是否在 slice 中。
func InSlice(items []string, item string) bool {
	for _, eachItem := range items {
		if eachItem == item {
			return true
		}
	}
	return false
}

func GetAllDir(pathname string, s []string) ([]string, error) {
	rd, err := os.ReadDir(pathname)
	if err != nil {
		log.Println("read dir fail:", err)
		return s, err
	}

	for _, fi := range rd {
		if fi.IsDir() {
			fullName := fi.Name()
			s = append(s, fullName)
		}
	}
	return s, nil
}

func GetAllFile(pathname string, s []string) ([]string, error) {
	rd, err := os.ReadDir(pathname)
	if err != nil {
		log.Println("read dir fail:", err)
		return s, err
	}

	for _, fi := range rd {
		if !fi.IsDir() {
			fullName := fi.Name()
			s = append(s, pathname+"/"+fullName)
		}
	}
	return s, nil
}

// FillYear 年份未知时补全年份
func FillYear(stamp time.Time) int64 {
	if stamp.Year() == 0 {
		nowStamp := time.Now()
		var y int
		if stamp.Month() > nowStamp.Month() {
			y = nowStamp.Year() - 1
		} else {
			y = nowStamp.Year()
		}
		stamp = stamp.AddDate(y, 0, 0)
	}
	return stamp.Unix()
}
