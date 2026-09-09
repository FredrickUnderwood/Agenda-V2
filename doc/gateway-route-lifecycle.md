# 网关路由的禁用与删除

应用的 Routes 列表通过 `GET /api/v1/applications/:appID/routes` 独立查询路由，
包含启用、禁用，以及所属环境已无实例的记录，不再从实例列表拼装。
已有的残留路由会直接出现在列表中，无需迁移数据。

| 操作 | 配置记录 | host/path 和 route key | 运行网关 |
| --- | --- | --- | --- |
| Disable | 保留，`enabled=false` | 继续保留占用 | 禁止新请求匹配该路由 |
| Delete | 物理删除路由及所选后端关联 | 释放，可重新创建 | 删除路由、后端和该路由的回滚历史，并刷新路由表 |
| Decommission instance | 保留实例及路由配置 | 继续保留占用 | 排空该实例；没有可用后端的路由停止提供服务 |
| 删除环境最后一个实例 | 保留路由，设为禁用 | 继续保留占用 | 实例删除仍要求先 decommission |

Decommission 不等于手动禁用路由配置：正常重新部署实例后，启用的路由配置可以恢复服务。
手动禁用的路由不会因其他实例 decommission 而重新启用。
路由编辑和重新启用仍需要该环境有实例，保存启用配置后随部署同步；无实例的路由可以直接删除。
禁用或删除会阻止新的路由匹配，已经建立的请求或 WebSocket 连接不会被强制切断。

控制面提供两个明确的写接口：

- `POST /api/v1/applications/:appID/routes/:routeID/disable`
- `DELETE /api/v1/applications/:appID/routes/:routeID`

写操作校验路由所属应用，并与该环境实例的部署、下线流程互斥。
网关集成开启时，必须先收到运行网关的确认，才修改本地记录。
网关调用失败时保留本地记录；本地写入失败时也可以重试。
删除不影响应用的部署日志和 release 历史。

网关新增 `POST /-/routes/:routeKey/disable` 和 `DELETE /-/routes/:routeKey`，
沿用 `route.update` 权限，请求体为 `{"application_id":11,"env":"test"}`。
网关校验记录归属，记录不存在时幂等返回 204，并刷新内存路由表。

升级时先发布 agenda-gateway，再发布 agenda-v2 与前端。旧网关未实现这些接口时，
控制面会返回失败并保留占用，不会把未知的 404 当成删除成功。
