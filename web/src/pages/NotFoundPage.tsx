import { Button, Result } from 'antd'
import { useNavigate } from 'react-router-dom'

export function NotFoundPage() {
  const navigate = useNavigate()
  return (
    <Result
      status="404"
      title="Nothing here"
      subTitle="That page doesn't exist, or you don't have access to it."
      extra={
        <Button type="primary" onClick={() => navigate('/applications')}>
          Back to applications
        </Button>
      }
    />
  )
}
