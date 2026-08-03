import { useEffect, useMemo } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, Popconfirm, Space, Tag, Typography } from 'antd'
import * as api from '@/api/settings'
import { errorMessage } from '@/utils/errorMessage'
import type { SettingFieldSpec, SettingSectionSpec } from './registry'
import { SETTINGS_QUERY_KEY, useSettings } from './useSettings'

type FormValues = Record<string, string>

// A save operation resolved from diffing the form against the stored settings.
type Op = { key: string; action: 'upsert'; field: SettingFieldSpec; value: string } | { key: string; action: 'delete' }

// Renders one section of well-known keys as a traditional labeled form. Non-secret
// values are prefilled from the store; secret values are never returned by the API
// (redacted to "***"), so their inputs stay blank and only a freshly typed value is
// ever written — an empty secret input means "keep the current value".
export function FixedSettingsForm({ section }: { section: SettingSectionSpec }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const { byKey, isLoading } = useSettings()
  const [form] = Form.useForm<FormValues>()

  // Baseline the form reflects: non-secret keys prefilled, secret keys blank.
  const baseline = useMemo(() => {
    const out: FormValues = {}
    for (const f of section.fields) {
      out[f.key] = f.secret ? '' : (byKey.get(f.key)?.value ?? '')
    }
    return out
    // Re-derive whenever any of this section's settings change (updated_at moves
    // on every save), which also re-blanks secret inputs after a successful save.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [section, section.fields.map((f) => `${f.key}=${byKey.get(f.key)?.updated_at ?? ''}`).join('|')])

  useEffect(() => {
    form.setFieldsValue(baseline)
  }, [baseline, form])

  const saveMutation = useMutation({
    mutationFn: async (ops: Op[]) => {
      for (const op of ops) {
        if (op.action === 'delete') {
          await api.deleteSetting(op.key)
        } else {
          await api.upsertSetting(op.key, { value: op.value, type: op.field.type, is_secret: op.field.secret })
        }
      }
      return ops.length
    },
    onSuccess: (n) => {
      message.success(n === 1 ? 'Saved 1 change.' : `Saved ${n} changes.`)
      queryClient.invalidateQueries({ queryKey: SETTINGS_QUERY_KEY })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (key: string) => api.deleteSetting(key),
    onSuccess: () => {
      message.success('Setting removed.')
      queryClient.invalidateQueries({ queryKey: SETTINGS_QUERY_KEY })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  function onFinish(values: FormValues) {
    const ops: Op[] = []
    for (const f of section.fields) {
      const current = (values[f.key] ?? '').trim()
      const existing = byKey.get(f.key)
      if (f.secret) {
        // Can't read a secret back, so blank = "leave as-is"; only a typed value writes.
        if (current !== '') ops.push({ key: f.key, action: 'upsert', field: f, value: current })
        continue
      }
      const stored = existing?.value ?? ''
      if (current === stored) continue
      if (current === '' && existing) ops.push({ key: f.key, action: 'delete' })
      else ops.push({ key: f.key, action: 'upsert', field: f, value: current })
    }
    if (ops.length === 0) {
      message.info('No changes to save.')
      return
    }
    saveMutation.mutate(ops)
  }

  return (
    <div style={{ maxWidth: 640 }}>
      <Typography.Title level={4} className="agenda-display" style={{ marginTop: 0 }}>
        {section.title}
      </Typography.Title>
      <Typography.Paragraph type="secondary">{section.description}</Typography.Paragraph>

      <Form form={form} layout="vertical" requiredMark="optional" onFinish={onFinish} disabled={isLoading}>
        {section.fields.map((f) => {
          const configured = byKey.has(f.key)
          // A required secret that's already set needn't be re-entered every save.
          const required = f.required && !(f.secret && configured)
          return (
            <Form.Item
              key={f.key}
              name={f.key}
              label={
                <Space size={8}>
                  <span>{f.label}</span>
                  {configured ? (
                    <Tag color="green" style={{ marginInlineEnd: 0 }}>
                      set
                    </Tag>
                  ) : null}
                  {f.secret ? <Tag style={{ marginInlineEnd: 0 }}>secret</Tag> : null}
                </Space>
              }
              rules={required ? [{ required: true, message: `${f.label} is required` }] : []}
              extra={
                <span>
                  <span className="agenda-mono" style={{ fontSize: 12, opacity: 0.7 }}>
                    {f.key}
                  </span>
                  {f.help ? <> — {f.help}</> : null}
                  {f.secret && configured ? <> Leave blank to keep the current value.</> : null}
                  {configured ? (
                    <>
                      {' '}
                      <Popconfirm title={`Remove ${f.key}?`} onConfirm={() => deleteMutation.mutate(f.key)}>
                        <Button type="link" size="small" danger style={{ padding: 0, height: 'auto' }}>
                          Remove
                        </Button>
                      </Popconfirm>
                    </>
                  ) : null}
                </span>
              }
            >
              {f.input === 'password' ? (
                <Input.Password className="agenda-mono" autoComplete="new-password" placeholder={f.secret && configured ? '••••••••' : f.placeholder} />
              ) : f.input === 'textarea' ? (
                <Input.TextArea className="agenda-mono" rows={2} placeholder={f.placeholder} />
              ) : (
                <Input className="agenda-mono" placeholder={f.placeholder} />
              )}
            </Form.Item>
          )
        })}

        <Form.Item style={{ marginTop: 8 }}>
          <Space>
            <Button type="primary" htmlType="submit" loading={saveMutation.isPending}>
              Save changes
            </Button>
            <Button onClick={() => form.setFieldsValue(baseline)} disabled={saveMutation.isPending}>
              Reset
            </Button>
          </Space>
        </Form.Item>
      </Form>
    </div>
  )
}
