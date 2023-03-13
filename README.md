# searchlog_agent
分布式的日志检索与收集客户端，海量设备日志随用随取，无需搭建大规模存储集群，低成本，适合运维人员使用。
Searchlog Agent需搭配ES使用，采集到的日志会存储到ES。
go语言编写，高性能，低资源消耗。

项目背景：CDN集群的设备分布在世界各地，我们无法将海量的日志进行实时采集，且大多数日志通常是没用的，偶尔需要使用时又无法做到快速检索、分析，给运维人员造成很大压力。
因此开发了searchlog agent来帮助运维人员快速检索分布在世界各地设备上的所需日志，大幅提高了定位与分析日志的效率。agent还集成了运行运维脚本的功能。

适合的使用场景：1、设备上的部分日志查询频率比较低，不值得去搭建ELK等通道实时上传，且运维/开发人员需要快速查询这部分日志来分析或定位故障的；
               2、原日志采集通道故障或高延迟，可通过此Agent来快速查询当前所需的日志；
               3、合适待采集日志的设备比较分散，且日志不需要全量采集（如error日志，主要在故障发生后需查询一段时间内的），通过ELK等方式采集会消耗大量公网带宽、高存储成本，可使用此方式；
               4、通过ELK等方式采集后的日志不再是原始日志（可能剔除了部分不重要的字段），但偶尔仍需要检索原始日志进行分析的；
               5、适合集群设备数量众多，不清楚待检索的日志具体处于那个设备上时，可用此Agent同时批量检索大量的设备日志。
将searchlog agent部署在需要被检索/收集/分析日志的设备上，通过API接口（https协议）下发任务，


源码编译安装（Linux）：
1、安装go环境
2、在searchlog_agent目录下执行命令：cd src;go build;mv -f searchlog ../;cd ../

运行（Linux）：
./searchlog

配置文件目录config说明：

1、server.key和server.pem文件是用于开启HTTPS协议的证书文件，当前为样例，请自行生成后替换。

2、config.ini文件

[All]                           # 公共配置
allowIps=127.0.0.1,127.0.0.2    # 允许访问的IP白名单，填写server端的IP地址，多个之间用逗号分隔。不在白名单内的地址访问会返回403。
listenIp=10.10.10.63            # 监听的本机地址
listenPort=8000                 # 监听的本机端口

[LogSearch]                     # 日志检索配置
maxCount=1000                   # 每次检索的最大符合条件的日志条数
retUrl=https://127.0.0.1:8000   # 检索完成后调用的URL，告知本次任务已完成
esHost=https://gcp.cloud.es.io  # 用于保存检索日志的ES地址
esUser=elastic                  # 用于保存检索日志的ES用户名
esPass=LO7pH7JGmn4ED6ftrJlWoU   # 用于保存检索日志的ES密码

[RunScript]                     # 运行自定义脚本配置
scriptPath=script               # 脚本存放路径


接口说明：

GET  /agent/isAvailable       # 测试此Agent是否可用

POST /agent/log/freeSearch    # 日志检索

POST /agent/run/script        # 运行自定义脚本配置
