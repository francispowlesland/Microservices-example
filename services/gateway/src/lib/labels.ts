import { PaymentStatus } from '#lib/gen/payments/v1/payments_pb.js';
import { UserStatus } from '#lib/gen/user/v1/user_pb.js';

type Tone = 'good' | 'warn' | 'bad' | 'quiet';

export const paymentStatus: Record<PaymentStatus, [string, Tone]> = {
	[PaymentStatus.UNSPECIFIED]: ['Unknown', 'quiet'],
	[PaymentStatus.PENDING]: ['Pending', 'warn'],
	[PaymentStatus.SUCCEEDED]: ['Paid', 'good'],
	[PaymentStatus.FAILED]: ['Declined', 'bad'],
	[PaymentStatus.REFUNDED]: ['Refunded', 'quiet']
};

export const userStatus: Record<UserStatus, [string, Tone]> = {
	[UserStatus.UNSPECIFIED]: ['Unknown', 'quiet'],
	[UserStatus.PENDING_VERIFICATION]: ['Awaiting verification', 'warn'],
	[UserStatus.ACTIVE]: ['Active', 'good'],
	[UserStatus.SUSPENDED]: ['Suspended', 'bad']
};

export function stockTone(quantity: number): [string, Tone] {
	if (quantity === 0) return ['Out of stock', 'bad'];
	if (quantity < 10) return ['Running low', 'warn'];
	return ['In stock', 'good'];
}
