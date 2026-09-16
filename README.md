# capture-control

A Viam sensor module that controls the [data manager's](https://docs.viam.com/services/data/) capture behavior at runtime. It emits per-resource **capture overrides** and **sequence** entries from its `Readings()`, and exposes `DoCommand` handlers to start/stop capture and open/close sequences on demand.

Use it when you want to gate data capture on external events (a button press, a robot state change, a workflow step) instead of relying on a static capture frequency in the data manager config.

## Models

| Model | API |
|---|---|
| [`viam:capture-control:capture-control-sensor`](viam_capture-control_capture-control-sensor.md) | `rdk:component:sensor` |

## How it works

The data manager polls this sensor's `Readings()` and reads two keys:

- **`overrides`** — one entry per configured `resource_name` / `method` pair, with the current `capture_frequency_hz` and `tags`. A frequency of `0` disables capture for that resource/method.
- **`sequences`** — while a sequence is open, one entry listing the same resource/method pairs plus the current `sequence_tags`. An empty list closes any open sequences.

You change what those readings emit by calling `DoCommand` on this sensor.

## Configuration

```json
{
  "resources": [
    { "resource_name": "camera-1", "method": "ReadImage" },
    { "resource_name": "sensor-1", "method": "Readings" }
  ],
  "default_capture_frequency_hz": 0,
  "default_tags": ["baseline"]
}
```

### Attributes

| Name | Type | Inclusion | Description |
|---|---|---|---|
| `resources` | array of `{resource_name, method}` | Required | Resource/method pairs whose data capture this sensor controls. Must contain at least one entry. |
| `default_capture_frequency_hz` | float | Optional | Capture frequency emitted until `start_capture` changes it. `0` (default) disables capture. |
| `default_tags` | array of string | Optional | Tags emitted on `overrides` until `start_capture` changes them. |

## DoCommand

Each command is selected by setting its key to `true`. Optional arguments are sibling keys on the same map.

### `start_capture`

Enables capture at `frequency_hz` (or `default_capture_frequency_hz` if omitted) with the given `tags`. Also opens a sequence with the same tags.

```json
{ "start_capture": true, "frequency_hz": 2.0, "tags": ["run-1"] }
```

### `stop_capture`

Sets the emitted frequency to `0` (disabling capture) and closes any open sequence.

```json
{ "stop_capture": true }
```

### `start_sequence`

Opens a sequence with the given `tags` without changing the capture frequency.

```json
{ "start_sequence": true, "tags": ["run-1"] }
```

### `stop_sequence`

Closes the open sequence. Capture frequency is unchanged.

```json
{ "stop_sequence": true }
```

## Build

```bash
make setup           # go mod tidy
make module.tar.gz   # produces the packaged module
```

See [`DEVELOPER_GUIDE.md`](DEVELOPER_GUIDE.md) for notes on the Viam Go module lifecycle.
