# 现场被动样本索引

所有样本均应记录：本地时间与 UTC、票务结果、CAN 方向、ID、长度、原始字节、屏幕剩余数量、杆是否动作及动作完成时间。监听必须只读，不能抢占 `/dev/ttyUSB2`。

## 已有样本

| 样本 | 票务结果 | CAN 观察 | 文件 |
|---|---|---|---|
| 无扫码基线 | 无 | 10 秒 0 帧 | `../../artifacts/gate-original/can-baseline-20260823.log` |
| 有效票 | 通过 | `0x122 → 0x123 → 0x122 → 0x123` | `../../artifacts/gate-original/can-scan-20260823.log` |
| 有效票第二次 | 通过 | 同一四帧结构，事务首字节继续变化 | `../../artifacts/gate-original/can-scan-next-20260823.log` |
| 两次感应 | 两次有效事务 | 共 8 帧，即两组四帧，不是一帧重复 | `../../artifacts/gate-original/joint-can-20260823.log` |
| 无效票 | 被拦截 | 0 帧；只新增查询记录 | `../../artifacts/gate-original/invalid-scan-20260823.md` |

## 有效票样本

```text
0x122 [8] 1d 13 00 00 45 3f c8 2f
0x123 [6] 21 ca 20 fe 41 68
0x122 [8] 00 00 e2 ed 88 7a c0 16
0x123 [8] 39 7c 83 fc 0c 6c ad e7
```

这些是加密/事务层样本，不能直接解释为票码或开闸 payload。

## 离线 fixture 校验

现有样本已固化为离线结构测试：

```sh
python docs/gate-original/tools/test_can_fixture_parser.py
```

当前 fixture 结果：

- `can-scan-20260823.log`：8 帧，2 组完整四帧事务；
- `can-scan-next-20260823.log`：4 帧，1 组完整四帧事务；
- `joint-can-20260823.log`：4 帧，1 组完整四帧事务；
- `can-baseline-20260823.log`：0 帧；
- 无效票对照：`invalid-scan-20260823.md` 记录 0 帧。

fixture 只检查 `0x122 → 0x123 → 0x122 → 0x123`、DLC 和原始字节保留，
不把解密 payload 映射为“开闸成功”，也不连接 `can0`。

## 下一轮样本模板

```text
sample_id:
local_time:
utc_time:
ticket_case: valid | duplicate | expired | refunded | invalid
ticket_result:
confirmbarticket_delta:
querybarticket_delta:
gate_display_before:
gate_display_after:
pole_started_at:
pole_finished_at:
can_direction_and_frames:
operator_observations:
```

优先补齐“已核销/重复票、过期票、退款票”对照；在协议闭合前不要用正式游客票做猜测性重放。
