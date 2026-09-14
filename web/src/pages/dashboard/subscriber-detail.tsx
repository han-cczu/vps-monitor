import { useParams } from 'src/routes/hooks';

import { SubscriberDetailView } from 'src/sections/subscribers/detail/subscriber-detail-view';

export default function Page() {
  const { id } = useParams();
  return <SubscriberDetailView id={Number(id)} />;
}
