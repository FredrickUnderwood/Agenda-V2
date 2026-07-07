import { useEffect } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, Select, Space, Typography } from 'antd'
import * as api from '@/api/applications'
import type { Application, UpdateApplicationRequest } from '@/api/types'
import { errorMessage } from '@/utils/errorMessage'

export function OverviewTab({ app }: { app: Application }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [form] = Form.useForm<UpdateApplicationRequest>()

  useEffect(() => {
    form.setFieldsValue({
      name: app.name,
      repo_url: app.repo_url,
      deploy_method: app.deploy_method,
      description: app.description,
      deploy_config: app.deploy_config,
    })
  }, [app, form])

  const updateMutation = useMutation({
    mutationFn: (req: UpdateApplicationRequest) => api.updateApplication(app.id, req),
    onSuccess: () => {
      message.success('Saved.')
      queryClient.invalidateQueries({ queryKey: ['applications', app.id] })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  return (
    <div style={{ maxWidth: 640 }}>
      <Form form={form} layout="vertical" requiredMark={false} onFinish={(v) => updateMutation.mutate(v)}>
        <Form.Item name="name" label="Name" rules={[{ required: true }]}>
          <Input />
        </Form.Item>
        <Form.Item name="repo_url" label="Repository URL" rules={[{ required: true }]}>
          <Input className="agenda-mono" />
        </Form.Item>
        <Form.Item name="deploy_method" label="Deploy method">
          <Select
            options={[
              { value: 'docker', label: 'Docker Compose' },
              { value: 'api', label: 'HTTP webhook' },
            ]}
          />
        </Form.Item>
        <Form.Item name="description" label="Description">
          <Input.TextArea rows={2} />
        </Form.Item>
        <Form.Item
          name="deploy_config"
          label="Deploy config (JSON)"
          extra="Docker: work_dir, compose_file, services, pre_commands, env, health_check. See doc/ for the full shape."
        >
          <Input.TextArea rows={10} className="agenda-mono" />
        </Form.Item>
        <Space>
          <Button type="primary" htmlType="submit" loading={updateMutation.isPending}>
            Save changes
          </Button>
        </Space>
      </Form>
      <Typography.Paragraph type="secondary" style={{ marginTop: 24, fontSize: 12 }}>
        Application #{app.id} · created {new Date(app.created_at).toLocaleString()}
      </Typography.Paragraph>
    </div>
  )
}
