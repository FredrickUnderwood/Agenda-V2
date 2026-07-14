import { useEffect } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { App, Button, Divider, Form, Input, Select, Space, Typography } from 'antd'
import * as api from '@/api/applications'
import type { Application, DeployMethod, UpdateApplicationRequest } from '@/api/types'
import { errorMessage } from '@/utils/errorMessage'
import { DeployConfigFields } from './DeployConfigFields'
import { buildDeployConfig, parseDeployConfig, type DeployConfigForm } from './deployConfig'

interface OverviewFormValues extends DeployConfigForm {
  name: string
  repo_url: string
  deploy_method: DeployMethod
  description?: string
}

export function OverviewTab({ app }: { app: Application }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [form] = Form.useForm<OverviewFormValues>()
  const method = Form.useWatch('deploy_method', form) ?? app.deploy_method

  useEffect(() => {
    const cfg = parseDeployConfig(app.deploy_config, app.deploy_method)
    form.setFieldsValue({
      name: app.name,
      repo_url: app.repo_url,
      deploy_method: app.deploy_method,
      description: app.description,
      docker: cfg.docker,
      api: cfg.api,
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

  const handleFinish = (v: OverviewFormValues) => {
    updateMutation.mutate({
      name: v.name,
      repo_url: v.repo_url,
      deploy_method: v.deploy_method,
      description: v.description,
      deploy_config: buildDeployConfig({ docker: v.docker, api: v.api }, v.deploy_method),
    })
  }

  return (
    <div style={{ maxWidth: 640 }}>
      <Form form={form} layout="vertical" requiredMark={false} onFinish={handleFinish}>
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

        <Divider titlePlacement="start" plain>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            Deploy config
          </Typography.Text>
        </Divider>
        <DeployConfigFields method={method} />

        <Space style={{ marginTop: 8 }}>
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
