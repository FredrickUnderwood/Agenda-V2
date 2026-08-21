import { Button, Form, Input, InputNumber, Select, Space, Switch } from 'antd'
import { MinusCircleOutlined, PlusOutlined } from '@ant-design/icons'
import type { DeployMethod } from '@/api/types'

// A repeated key/value editor backed by a Form.List of { key, value } rows.
// Used for docker env vars and api request headers — replaces hand-written JSON
// objects with a pair of inputs per row.
function KeyValueList({
  name,
  keyPlaceholder,
  valuePlaceholder,
  addLabel,
}: {
  name: (string | number)[]
  keyPlaceholder: string
  valuePlaceholder: string
  addLabel: string
}) {
  return (
    <Form.List name={name}>
      {(fields, { add, remove }) => (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {fields.map(({ key, name: fieldName, ...rest }) => (
            <Space key={key} align="baseline" style={{ display: 'flex' }}>
              <Form.Item {...rest} name={[fieldName, 'key']} noStyle>
                <Input placeholder={keyPlaceholder} className="agenda-mono" style={{ width: 200 }} />
              </Form.Item>
              <Form.Item {...rest} name={[fieldName, 'value']} noStyle>
                <Input placeholder={valuePlaceholder} className="agenda-mono" style={{ width: 260 }} />
              </Form.Item>
              <MinusCircleOutlined onClick={() => remove(fieldName)} />
            </Space>
          ))}
          <Button type="dashed" onClick={() => add()} icon={<PlusOutlined />} style={{ width: 'fit-content' }}>
            {addLabel}
          </Button>
        </div>
      )}
    </Form.List>
  )
}

// A repeated single-line editor backed by a Form.List of plain strings.
// Used for docker pre_commands, which may contain spaces (so tags won't do).
function StringList({ name, placeholder, addLabel }: { name: (string | number)[]; placeholder: string; addLabel: string }) {
  return (
    <Form.List name={name}>
      {(fields, { add, remove }) => (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {fields.map(({ key, name: fieldName, ...rest }) => (
            <Space key={key} align="baseline" style={{ display: 'flex' }}>
              <Form.Item {...rest} name={fieldName} noStyle>
                <Input placeholder={placeholder} className="agenda-mono" style={{ width: 468 }} />
              </Form.Item>
              <MinusCircleOutlined onClick={() => remove(fieldName)} />
            </Space>
          ))}
          <Button type="dashed" onClick={() => add()} icon={<PlusOutlined />} style={{ width: 'fit-content' }}>
            {addLabel}
          </Button>
        </div>
      )}
    </Form.List>
  )
}

function DockerFields() {
  return (
    <>
      <Form.Item name={['docker', 'work_dir']} label="Working directory" extra="Path on the machine where compose runs, relative to the checkout.">
        <Input placeholder="." className="agenda-mono" />
      </Form.Item>
      <Form.Item name={['docker', 'machine']} label="Machine" extra="Name of the config machine to deploy on. Leave blank for the target's own machine.">
        <Input placeholder="(target machine)" className="agenda-mono" />
      </Form.Item>
      <Form.Item name={['docker', 'compose_file']} label="Compose file" extra="Defaults to docker-compose.yml when blank.">
        <Input placeholder="docker-compose.yml" className="agenda-mono" />
      </Form.Item>
      <Form.Item name={['docker', 'services']} label="Services" extra="Compose services to deploy. Leave empty for all services.">
        <Select mode="tags" placeholder="Type a service name and press Enter" tokenSeparators={[',', ' ']} open={false} />
      </Form.Item>
      <Form.Item name={['docker', 'pull_before_deploy']} label="Pull images before deploy" valuePropName="checked">
        <Switch />
      </Form.Item>
      <Form.Item label="Pre-commands" extra="Shell commands run in the working directory before compose up.">
        <StringList name={['docker', 'pre_commands']} placeholder="npm ci && npm run build" addLabel="Add command" />
      </Form.Item>
    </>
  )
}

// Split out of DockerFields so OverviewTab can give it its own collapsible
// section. Only relevant for the docker deploy method.
export function DockerHealthCheckFields() {
  return (
    <>
      <Form.Item name={['docker', 'health_check', 'enabled']} label="Wait for containers to become healthy" valuePropName="checked">
        <Switch />
      </Form.Item>
      <Form.Item name={['docker', 'health_check', 'require_healthy']} label="Fail deploy if not healthy" valuePropName="checked">
        <Switch />
      </Form.Item>
      <Space size="large" wrap>
        <Form.Item name={['docker', 'health_check', 'timeout_seconds']} label="Timeout (seconds)">
          <InputNumber min={0} placeholder="default" style={{ width: 160 }} />
        </Form.Item>
        <Form.Item name={['docker', 'health_check', 'interval_seconds']} label="Interval (seconds)">
          <InputNumber min={0} placeholder="default" style={{ width: 160 }} />
        </Form.Item>
      </Space>
    </>
  )
}

function ApiFields() {
  return (
    <>
      <Form.Item name={['api', 'url']} label="Webhook URL" rules={[{ required: true, message: 'URL is required' }]}>
        <Input placeholder="https://deploy.example.com/hook" className="agenda-mono" />
      </Form.Item>
      <Form.Item name={['api', 'method']} label="HTTP method" initialValue="POST">
        <Select
          style={{ width: 160 }}
          options={['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map((m) => ({ value: m, label: m }))}
        />
      </Form.Item>
      <Form.Item label="Headers">
        <KeyValueList name={['api', 'headers']} keyPlaceholder="Header-Name" valuePlaceholder="value" addLabel="Add header" />
      </Form.Item>
      <Form.Item name={['api', 'body']} label="Request body">
        <Input.TextArea rows={4} className="agenda-mono" placeholder="Sent as-is (e.g. a JSON payload)." />
      </Form.Item>
      <Space size="large" wrap>
        <Form.Item name={['api', 'expected_status']} label="Expected status">
          <InputNumber min={0} max={599} placeholder="2xx" style={{ width: 160 }} />
        </Form.Item>
        <Form.Item name={['api', 'timeout_seconds']} label="Timeout (seconds)">
          <InputNumber min={0} placeholder="default" style={{ width: 160 }} />
        </Form.Item>
      </Space>
    </>
  )
}

export function DeployConfigFields({ method }: { method: DeployMethod }) {
  return method === 'api' ? <ApiFields /> : <DockerFields />
}
