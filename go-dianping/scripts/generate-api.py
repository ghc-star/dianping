"""Generate human/API specifications from one reviewed endpoint catalogue (stdlib only)."""
from pathlib import Path
import json, re
from urllib.parse import urlencode

root=Path(__file__).resolve().parents[1]
def ref(name): return {'$ref':'#/components/schemas/'+name}
def arr(name): return {'type':'array','items':ref(name)}
integer={'type':'integer','format':'int64'}
text={'type':'string'}
boolean={'type':'boolean'}
schemas={}
descriptions={'id':'主键；int64。订单 ID 可能超过 JavaScript 精确整数范围。','userId':'用户 ID','shopId':'商铺 ID','typeId':'商铺类型 ID','voucherId':'优惠券 ID','nickName':'昵称','icon':'头像/图标路径','name':'名称；Blog 中为作者昵称','images':'图片路径，多个以逗号分隔','x':'经度','y':'纬度','avgPrice':'整数均价（沿用原单位）','payValue':'支付金额，单位分','actualValue':'抵扣金额，单位分','liked':'点赞数','comments':'评论数','score':'评分乘 10；0–50','distance':'距查询点的距离，单位米','createTime':'创建时间，RFC3339 带时区','updateTime':'更新时间，RFC3339 带时区','stock':'剩余库存','beginTime':'开始时间','endTime':'结束时间','status':'状态：优惠券 1上架/2下架/3过期；订单1未支付/2已支付/3已核销/4取消/5退款中/6已退款','type':'0普通券/1秒杀券','payType':'1余额/2支付宝/3微信，仅存储字段，未实现支付接口','isLike':'当前用户是否点赞','gender':'0男/1女（沿用原数据）','level':'会员等级0–9','birthday':'生日；当前输出RFC3339日期时间','parentId':'一级评论ID；顶层为0','answerId':'回复评论ID','phone':'手机号，不在用户公开DTO返回','followUserId':'被关注用户ID','date':'日期','isBackup':'是否补签','fans':'粉丝数量（原用户资料字段，不随关注实时统计）','followee':'关注数（原资料字段）','credits':'积分','city':'城市','introduce':'简介','title':'标题','subTitle':'副标题','rules':'使用规则','content':'正文','area':'商圈','address':'地址','sold':'销量','openHours':'营业时间','payTime':'支付时间','useTime':'核销时间','refundTime':'退款时间'}
for name,body in re.findall(r'type (\w+) struct \{(.*?)\n\}',(root/'internal/model/models.go').read_text(),re.S):
    props={}
    for typ,tag in re.findall(r'\w+\s+([*\w.]+)\s+`[^`]*json:"([^" ,]+)(?:,[^"]*)?"[^`]*`',body):
        if tag=='-':continue
        s={'type':'string','format':'date-time'} if 'time.Time' in typ else dict(boolean if typ.endswith('bool') else integer if typ.lstrip('*') in ['int','int64'] else {'type':'number','format':'double'} if typ=='float64' else text)
        if typ.startswith('*'):s['nullable']=True
        s['description']=descriptions.get(tag,tag)
        props[tag]=s
    schemas[name]={'type':'object','properties':props}
schemas['UserDTO']={'type':'object','properties':{k:dict(integer if k=='id' else text,description=descriptions[k]) for k in ['id','nickName','icon']}}
schemas['LoginInput']={'type':'object','required':['phone'],'properties':{'phone':{'type':'string','pattern':'^1[3-9][0-9]{9}$'},'code':{'type':'string','pattern':'^[0-9]{6}$','description':'验证码优先于password；仅可成功使用一次'},'password':{'type':'string','minLength':8,'maxLength':72,'description':'新增bcrypt密码登录；先用验证码登录设置密码'}}}
schemas['PasswordInput']={'type':'object','required':['password'],'properties':{'password':{'type':'string','minLength':8,'maxLength':72,'description':'8–72字节'}}}
schemas['ShopInput']={'type':'object','properties':{k:v for k,v in schemas['Shop']['properties'].items() if k not in ['createTime','updateTime','distance']},'description':'POST必填name,typeId,address,x,y；PUT必填id和至少一个更新字段，0值可更新，省略字段保持。'}
schemas['BlogInput']={'type':'object','required':['shopId','title','images','content'],'properties':{k:schemas['Blog']['properties'][k] for k in ['shopId','title','images','content']}}
schemas['VoucherInput']={'type':'object','required':['shopId','title','payValue','actualValue'],'properties':{k:v for k,v in schemas['Voucher']['properties'].items() if k not in ['id','type','status','createTime','updateTime']},'description':'金额>0，秒杀额外必填stock>0、beginTime、endTime。接受带时区RFC3339或无时区2006-01-02T15:04:05（按配置时区）。'}
schemas['ScrollResult']={'type':'object','properties':{'list':arr('Blog'),'minTime':dict(integer,description='下一页lastId，最小毫秒时间戳'),'offset':{'type':'integer','description':'此minTime已消费条数，原样带到下一页'}}}
schemas['OrderStatus']={'type':'object','properties':{'id':dict(text,description='精确订单ID字符串'),'state':{'type':'string','enum':['pending','created','failed']},'error':text,'order':ref('VoucherOrder')}}
schemas['Error']={'type':'object','required':['success','errorMsg'],'properties':{'success':{'type':'boolean','enum':[False]},'errorMsg':text}}

E=[]
def endpoint(method,path,title,auth,out,example,redis,db,note='',query=(),body=None,request=None,error='请求参数无效',multipart=False):
    E.append(dict(method=method,path=path,title=title,auth=auth,out=out,example=example,redis=redis,db=db,note=note,query=query,body=body,request=request,error=error,multipart=multipart))
P=('current','integer',False,1,'页码；默认1。商铺上限1000，Blog上限100000。')
ID=('id','integer',True,1,'用户ID，正整数')
u={'id':1,'nickName':'学习用户一','icon':''}
shop={'id':1,'name':'示例商铺','typeId':1,'address':'示例地址','x':120.149192,'y':30.316078,'score':45}
blog={'id':1,'shopId':1,'userId':1,'title':'探店笔记','images':'/imgs/demo.png','content':'这是一篇学习笔记','liked':1,'name':'学习用户一','icon':'','isLike':True}
voucher={'id':2,'shopId':1,'title':'学习秒杀券','payValue':5000,'actualValue':10000,'type':1,'status':1,'stock':100,'beginTime':'2020-01-01T00:00:00+08:00','endTime':'2037-01-01T00:00:00+08:00'}
endpoint('post','/user/code','发送验证码',False,None,None,'String login:code:手机号 2m；login:throttle:手机号 60s','无','开发短信写API日志，不在响应中泄露。关闭dev_code_log时明确返回503，需接短信商。',query=[('phone','string',True,'13900000001','11位中国大陆手机号')],error='验证码发送频繁，请稍后重试')
endpoint('post','/user/login','验证码/密码登录',False,text,'64位随机十六进制Token','消费验证码；限制5次尝试；写login:token:Token Hash，TTL30m','按唯一手机号查询；首次验证码登录创建tb_user','裸Token返回data；password分支只验证bcrypt，不接受原未启用的MD5工具。',body='LoginInput',request={'phone':'13900000001','code':'从日志或Redis读取'},error='验证码无效或尝试次数过多')
endpoint('post','/user/logout','注销当前会话',True,None,None,'DEL当前Token Hash','无','修复Java仅清ThreadLocal却未删除登录态的问题。')
endpoint('post','/user/refresh','主动续期',True,{'type':'object','properties':{'ttlSeconds':integer}},{'ttlSeconds':1800},'中间件把当前Token TTL刷新至30m','无','新增；平常任意携带有效Token的请求也自动滑动续期。')
endpoint('get','/user/me','当前用户',True,ref('UserDTO'),u,'读取并刷新当前Token Hash','无')
endpoint('get','/user/{id}','查询用户公开信息',True,ref('UserDTO'),u,'仅会话续期','SELECT tb_user','不存在返回success:true无data，不返回phone/password。')
endpoint('get','/user/info/{id}','用户资料',True,ref('UserInfo'),{'userId':1,'city':'杭州','gender':0,'level':0},'仅会话续期','SELECT tb_user_info','无资料时无data；不返回创建/更新时间。')
endpoint('put','/user/password','设置当前用户密码',True,None,None,'仅会话续期','UPDATE tb_user.password 为bcrypt哈希','新增学习接口；当前有效登录态作为授权。',body='PasswordInput',request={'password':'learning-go-2026'})
endpoint('post','/user/sign','今日签到',True,None,None,'SETBIT sign:用户ID:yyyyMM，当月第day-1位','无','按Asia/Shanghai自然日；重复签到幂等，不写tb_sign。')
endpoint('get','/user/sign/count','连续签到天数',True,integer,3,'BITFIELD sign:用户ID:yyyyMM GET u日数 0','无','从今天向前数末尾连续1，今天未签到返回0，不跨月。')
endpoint('get','/shop/{id}','商铺详情',False,ref('Shop'),shop,'Cache Aside cache:shop:ID；空值2m，正常30–33m逻辑TTL+5m过期缓冲','未命中查询tb_shop','冷缓存自动回源；singleflight+锁合并；更新版本防止旧数据回填。',error='资源不存在')
endpoint('post','/shop','创建商铺',True,integer,15,'维护shop:geo:类型ID；失效对应缓存','INSERT tb_shop，校验tb_shop_type','原匿名写接口改为须登录。',body='ShopInput',request={'name':'新商铺','typeId':1,'address':'示例路1号','x':120.15,'y':30.31})
endpoint('put','/shop','更新商铺',True,None,None,'提交后删除缓存并递增版本；维护GEO','UPDATE tb_shop 白名单字段','原匿名写接口改为须登录。支持score:0；缓存删除失败会返回503并提示DB已更新，可重试相同更新。',body='ShopInput',request={'id':1,'name':'新名称','score':0})
endpoint('get','/shop/of/type','类型/附近商铺',False,arr('Shop'),[dict(shop,distance=120.5)],'提供x/y时GEOSEARCH，5000米、ASC距离排序；必要时重建索引','类型过滤或按GEO返回ID回查tb_shop','每页5条；必须同时提供x/y，不提供走普通分类查询。',query=[('typeId','integer',True,1,'商铺类型正整数'),P,('x','number',False,120.149192,'经度-180..180'),('y','number',False,30.316078,'纬度-85.05112878..85.05112878')])
endpoint('get','/shop/of/name','名称搜索商铺',False,arr('Shop'),[shop],'无（有效Token仍续期）','tb_shop.name LIKE，分页10条','无name时按ID分页全部商铺。',query=[('name','string',False,'美食','名称关键词'),P])
endpoint('get','/shop-type/list','商铺类型列表',False,arr('ShopType'),[{'id':1,'name':'美食','icon':'/types/ms.png','sort':1}],'shop_type: 缓存包裹JSON String，30–33m+5m','未命中tb_shop_type ORDER BY sort,id','替代原无TTL且可能重复的List，接口数组不变。')
endpoint('get','/blog/hot','热门笔记',False,arr('Blog'),[blog],'仅可选Token续期','tb_blog按liked desc,id desc；批量查作者/点赞明细','每页10条。游客isLike=false。',query=[P])
endpoint('get','/blog/{id}','笔记详情',True,ref('Blog'),blog,'仅会话续期','tb_blog + tb_user + tb_blog_like','原实现除hot外所有Blog查询均要求登录。')
endpoint('post','/blog','发布笔记',True,integer,5,'尝试将blogId以毫秒score写入粉丝feed:ID ZSet','同事务写tb_blog与tb_feed_outbox','最多9张图片；作者强制当前用户。Redis失败后由outbox补投。',body='BlogInput',request={k:blog[k] for k in ['shopId','title','images','content']})
endpoint('put','/blog/like/{id}','切换点赞',True,None,None,'从持久明细更新blog:liked:ID ZSet','锁定Blog行，事务切换tb_blog_like并更新liked','重复请求会切换回取消；该接口不是幂等PUT语义，沿用原路由，网络未知结果不要盲重试。')
endpoint('get','/blog/likes/{id}','最早点赞用户',True,arr('UserDTO'),[u],'重建blog:liked:ID；ZRANGE 0 4','读取持久点赞明细及用户','返回最早5个赞；同毫秒按Redis成员字典序，Redis失败用DB结果。')
endpoint('get','/blog/of/me','我的笔记',True,arr('Blog'),[blog],'仅会话续期','tb_blog按当前user_id，id desc','每页10条；此接口沿用原未富化的Blog列表。',query=[P])
endpoint('get','/blog/of/user','指定用户笔记',True,arr('Blog'),[blog],'仅会话续期','tb_blog按目标user_id，id desc','每页10条。',query=[ID,P])
endpoint('get','/blog/of/follow','关注Feed滚动分页',True,ref('ScrollResult'),{'list':[blog],'minTime':1788790000000,'offset':1},'ZREVRANGEBYSCORE feed:当前用户；可从持久outbox恢复','按ID有序回查Blog并富化作者点赞','每页2条；下一页带minTime作为lastId和offset原值；到底返回success:true无data。默认lastId为当前毫秒。',query=[('lastId','integer',False,1788790000000,'最大毫秒时间戳，包含该分值'),('offset','integer',False,0,'该分值已消费数量；0..100000')])
endpoint('put','/follow/{id}/{isFollow}','关注/取消关注',True,None,None,'刷新follows:当前用户 Set','tb_follow唯一(user_id,follow_user_id)，true插入false删除','目标用户须存在且不能关注自己；重复true/false幂等。')
endpoint('get','/follow/or/not/{id}','是否关注',True,boolean,True,'仅会话续期','查询tb_follow')
endpoint('get','/follow/common/{id}','共同关注',True,arr('UserDTO'),[u],'重建两用户Set并SINTER','锁定用户行顺序读取tb_follow，回查用户','Redis不可用时回退持久关注交集。')
endpoint('post','/voucher','新增普通优惠券',True,integer,3,'不写秒杀库存','INSERT tb_voucher，type=0,status=1','原匿名写接口改为须登录。',body='VoucherInput',request={'shopId':1,'title':'普通券','payValue':8000,'actualValue':10000})
endpoint('post','/voucher/seckill','新增秒杀券',True,integer,4,'初始化库存String、时间Hash、已购Hash','事务写tb_voucher及tb_seckill_voucher','原匿名写改须登录。DB已提交而Redis初始化失败时需按SECKILL.md修复，勿直接重复新增。',body='VoucherInput',request={'shopId':1,'title':'秒杀券','payValue':5000,'actualValue':10000,'stock':10,'beginTime':'2020-01-01T00:00:00+08:00','endTime':'2037-01-01T00:00:00+08:00'})
endpoint('get','/voucher/list/{shopId}','商铺优惠券列表',False,arr('Voucher'),[voucher],'无','tb_voucher LEFT JOIN tb_seckill_voucher；status=1','保持原查询规则，不额外按时间过滤。')
endpoint('post','/voucher-order/seckill/{id}','秒杀下单',True,{'oneOf':[integer,text]},'633000000000000001','Lua检查时间、库存、一人一单并原子预扣+XADD+pending状态','请求通常不写DB；后台事务条件扣库存+插订单','返回表示已受理，非已持久化。默认数字保持Java兼容；传idAsString=true返回精确字符串。响应头X-Order-ID始终提供精确字符串。',query=[('idAsString','boolean',False,True,'true时data为精确十进制字符串；浏览器新代码建议开启')],error='库存不足')
endpoint('get','/voucher-order/{id}','查询自己的已落库订单',True,ref('VoucherOrder'),{'id':633000000000000001,'userId':1,'voucherId':2,'status':1,'payType':1},'仅会话续期','SELECT tb_voucher_order WHERE id AND user_id','新增；未落库/不属于当前用户都返回资源不存在。')
endpoint('get','/voucher-order/status/{id}','查询异步订单状态',True,ref('OrderStatus'),{'id':'633000000000000001','state':'pending'},'DB无订单时读取订单状态Hash','优先MySQL权威订单','新增；pending/created/failed；状态id是字符串。')
endpoint('post','/upload/blog','上传笔记图片',True,text,'/blogs/1/'+'a'*64+'.png','仅会话续期','无；写配置目录文件','原匿名改登录；最多5MiB，按文件内容识别PNG/JPEG/GIF/WebP；返回路径供/imgs前缀使用。',body=None,multipart=True,error='只支持 PNG/JPEG/GIF/WebP 图片')
endpoint('delete','/upload/blog/delete','删除本人图片',True,None,None,'仅会话续期','无；删除当前用户目录中的图片','原匿名删除改为登录且只允许本人新格式文件；旧图片可保留读取，由运维迁移，不提供任意路径删除。',query=[('name','string',True,'/blogs/1/'+'a'*64+'.png','上传接口返回的完整name')])
for path,label in [('/health/live','存活检查'),('/health/ready','就绪检查')]:endpoint('get',path,label,False,{'type':'object','properties':{'status':text}},{'status':'up' if path.endswith('live') else 'ready'},'ready检查PING，live不检查','ready检查连接池PING，live不检查','运维接口；依赖不可用ready返回503。')

spec={'openapi':'3.0.3','info':{'title':'Go 黑马点评 API','version':'1.0.0','description':'保留Java业务Result格式。教学写接口需登录但没有商家RBAC；公开部署前应补角色权限。金额为分；ID为int64，秒杀响应X-Order-ID为精确字符串。'},'servers':[{'url':'http://localhost:8081'},{'url':'http://localhost:8080/api'}],'paths':{},'components':{'securitySchemes':{'token':{'type':'apiKey','in':'header','name':'authorization','description':'直接填Token；也支持Bearer Token'}},'schemas':schemas}}
md=['# 接口文档\n\n本文件与 `api/openapi.yaml` 由 `scripts/generate-api.py` 的已核对接口目录生成。OpenAPI文件使用JSON语法（合法YAML 1.2），可直接导入Swagger/Postman。API根地址 `http://localhost:8081`；Nginx兼容根地址 `http://localhost:8080/api`。\n',
'## 统一约定\n\n成功 `{ "success": true, "data": ... }`；无返回值省略data。错误 `{ "success": false, "errorMsg": "..." }`。保留可选total字段，但原分页接口没有返回总数，本实现也不虚构。业务错误HTTP200，认证401，未知接口404，系统故障500，暂时不可用503。所有受保护接口Headers：`authorization: TOKEN`（也支持Bearer），JSON请求加`Content-Type: application/json`。有效Token每次访问自动滑动续期30分钟。公共路由携带无效Token按游客，携带Token遇Redis故障会报错。\n\n金额使用整数分；坐标是浮点度数。JSON时间输出RFC3339带时区，输入秒杀时间兼容Java无时区字符串。浏览器调用秒杀接口应传`idAsString=true`，也可读取`X-Order-ID`响应头；不要先把默认数值data转成Number再转回字符串。成功仅代表异步受理，轮询订单状态确认落库。\n',
'## 与 Java 的变化\n\n| 原接口 | Go接口 | 变化及前端动作 |\n|---|---|---|\n| 商铺/券/上传写接口匿名 | URL和Method不变 | 增加authorization；上传删除限本人新格式路径 |\n| /user/logout | 不变 | 真正删除Redis Token，清除前端本地Token |\n| /user/login | 不变 | 验证码单次有效、5次尝试、60秒发送间隔；新增bcrypt密码分支 |\n| Java LocalDateTime/Date | 不变 | 输出带时区，前端日期展示需正确解析；生日显示取日期 |\n| 数字订单ID | 不变 | 新增idAsString=true和X-Order-ID精确字符串；旧前端仍可读默认数字 |\n| 无订单查询 | GET /voucher-order/{id}、/status/{id} | 新增受理结果查询 |\n| 无主动续期/密码设置 | POST /user/refresh、PUT /user/password | 可选新增，不影响验证码登录 |\n\n除hot外Blog读取、用户资料、关注路由原本就需登录。原BlogCommentsController无接口，本项目没有伪造评论/支付/退款功能。所有已有有效业务URL均保留。\n']
for i,e in enumerate(E,1):
    params=[]
    for key in re.findall(r'\{(.*?)\}',e['path']):params.append({'name':key,'in':'path','required':True,'schema':boolean if key=='isFollow' else dict(integer,minimum=1),'description':'true关注/false取消' if key=='isFollow' else '目标资源ID，正整数','example':True if key=='isFollow' else 1})
    for key,typ,required,ex,desc in e['query']:params.append({'name':key,'in':'query','required':required,'schema':{'type':typ},'description':desc,'example':ex})
    success={'type':'object','required':['success'],'properties':{'success':{'type':'boolean','enum':[True]}}}
    example={'success':True}
    if e['out'] is not None:success['properties']['data']=e['out'];example['data']=e['example']
    operation={'summary':e['title'],'operationId':re.sub(r'[^a-zA-Z0-9_]','_',e['method']+'_'+e['path']),'tags':[e['path'].split('/')[1]],'security':[{'token':[]}] if e['auth'] else [],'description':e['note']+'\nRedis: '+e['redis']+'\nMySQL: '+e['db'],'parameters':params,'responses':{'200':{'description':'业务结果；检查success字段','content':{'application/json':{'schema':{'oneOf':[success,ref('Error')]},'examples':{'success':{'value':example},'failure':{'value':{'success':False,'errorMsg':e['error']}}}}}},'401':{'description':'缺少或失效Token','content':{'application/json':{'schema':ref('Error')}}},'500':{'description':'系统故障','content':{'application/json':{'schema':ref('Error')}}},'503':{'description':'依赖或短信服务不可用','content':{'application/json':{'schema':ref('Error')}}}}}
    if e['path']=='/voucher-order/seckill/{id}':operation['responses']['200']['headers']={'X-Order-ID':{'description':'成功受理时返回精确订单ID字符串','schema':text}}
    if e['body']:operation['requestBody']={'required':True,'content':{'application/json':{'schema':ref(e['body']),'example':e['request']}}}
    if e['multipart']:operation['requestBody']={'required':True,'content':{'multipart/form-data':{'schema':{'type':'object','required':['file'],'properties':{'file':{'type':'string','format':'binary'}}}}}}
    spec['paths'].setdefault(e['path'],{})[e['method']]=operation
    url=re.sub(r'\{(.*?)\}',lambda m:'true' if m[1]=='isFollow' else '1',e['path'])
    if params and e['query']:url+='?'+urlencode({k:(str(ex).lower() if isinstance(ex,bool) else ex) for k,typ,req,ex,desc in e['query']})
    curl=f"curl -X {e['method'].upper()} 'http://localhost:8081{url}'"
    if e['auth']:curl+=" -H 'authorization: TOKEN'"
    if e['body']:curl+=" -H 'Content-Type: application/json' -d '"+json.dumps(e['request'],ensure_ascii=False)+"'"
    if e['multipart']:curl+=" -F 'file=@photo.png'"
    paramtable='\n'.join(f"| {p['in']} | {p['name']} | {p['schema']['type']} | {'是' if p['required'] else '否'} | {p['description']} |" for p in params) or '| — | — | — | — | 无Path/Query参数 |'
    md.append(f"## {i}. {e['title']}\n\n`{e['method'].upper()} {e['path']}`；登录：{'必须' if e['auth'] else '不要求'}。\n\n功能与备注：{e['note'] or e['title']+'，保持原接口用途。'}\n\nHeaders：{'authorization: TOKEN；' if e['auth'] else '可选authorization；'}{'Content-Type: application/json' if e['body'] else 'multipart/form-data（由客户端生成boundary）' if e['multipart'] else '无Content-Type要求'}。\n\n| 位置 | 参数 | 类型 | 必填 | 说明 |\n|---|---|---|---|---|\n{paramtable}\n\nBody：{e['body']+'（字段见下方数据结构表及OpenAPI）' if e['body'] else 'file二进制图片，必填' if e['multipart'] else '无'}。\n\n```bash\n{curl}\n```\n\n成功示例（data字段结构参见数据结构表）：\n\n```json\n{json.dumps(example,ensure_ascii=False,indent=2)}\n```\n\n错误示例：`{json.dumps({'success':False,'errorMsg':e['error']},ensure_ascii=False)}`。未登录为HTTP401；DB/Redis故障按统一规则返回500/503。\n\nRedis影响：{e['redis']}。\n\n数据库/文件影响：{e['db']}。\n")
md.append('## 数据结构与响应字段\n\n每个接口的顶层字段遵循统一Result；以下表格说明data的对象字段。数组接口返回这些对象的数组。请求Body只允许接口注明字段，服务端ID/作者/计数/创建时间不能由客户端覆盖。\n')
for name,schema in schemas.items():
    md.append(f'### {name}\n\n{schema.get("description","")}\n\n| 字段 | 类型 | 说明 |\n|---|---|---|\n')
    for key,p in schema.get('properties',{}).items():md.append(f'| {key} | {p.get("type",p.get("$ref","对象").split("/")[-1])} | {p.get("description",descriptions.get(key,key))} |\n')
md.append('\n## 文档与静态资源\n\nGET `/docs` 提供无外部CDN依赖的接口目录；GET `/api/openapi.yaml` 下载规范；GET/HEAD `/imgs/{path}` 读取上传目录静态图片。原前端HTML不在仓库中，Nginx只提供反向代理。\n')
(root/'api').mkdir(exist_ok=True)
(root/'api/openapi.yaml').write_text(json.dumps(spec,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
(root/'docs/API.md').write_text(''.join(md),encoding='utf-8')
print(f'Generated {len(E)} documented operations.')
