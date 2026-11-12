# Monica2API Cookie Sync（浏览器扩展）

一键把当前浏览器里已登录的 monica.im Cookie 发送给你自己的 Monica2API 代理。

## 安装（Chrome / Edge / Ego 等 Chromium 浏览器）
1. 打开 `chrome://extensions`，开启右上角「开发者模式」
2. 点「加载已解压的扩展程序」，选择本目录 `extension/monica-cookie-sync`
3. 把扩展固定到工具栏

## 使用
1. 在同一个浏览器里登录 https://monica.im
2. 点扩展图标，填写代理地址（默认 `https://monica2api.onrender.com`）和管理员密码
3. 点「读取 monica.im Cookie 并更新」

代理会先用新 Cookie 向 Monica 验证，验证通过才会替换，并立即生效。

## 安全说明
- 扩展只读取 monica.im 的 Cookie，只发送到你填写的代理地址
- 管理员密码默认不保存；勾选「记住」时只保存在浏览器会话存储里，关闭浏览器即清除
- 代理端对密码错误有限速：同一 IP 连续错 5 次锁 15 分钟，全局 1 小时内错 20 次暂停
